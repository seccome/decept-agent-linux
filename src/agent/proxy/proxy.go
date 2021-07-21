package main

import (
	"C"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gomodule/redigo/redis"
	"os"
	"strings"
	"time"
	"util/comm"
	"util/logger"
)

var (
	AgentHome        = comm.AgentHome()
	Proxy            = "/proxy/tcpProxy"
	ProxyLogConfPath = fmt.Sprintf("%s%s", AgentHome, "/proxy/conf/log.json")
	AgentConf        comm.AgentConf
	ProxySubChannel  = "proxy-strategy-sub-channel"
	ProxyPubChannel  = "proxy-strategy-pub-channel"
	ProxyTablePath   = AgentHome + "/proxy/proxy-table.json"
	TimeFormat       = "2006-01-02 15:04:05"
)

/*

	负责监控
		1.代理策略更新
		2.代理执行反馈
		3.代理启动
*/
func main() {

	err := logger.SetLogger(ProxyLogConfPath)

	if err != nil {
		// TODO 文件夹错误或读取异常
		logger.Error("logger setting err: ", err)
	}
	AgentConf = comm.LoadAgentConf(AgentHome)
	comm.ReadProxyTable() // 触发proxy-table.json 创建
	go comm.MonitForKillSelfTask()

	go comm.StartMemCpuMonitor("proxy", 5)

	go zombieProcessClean()

	client := comm.NewRedis(AgentConf.StrategyAddr, AgentConf.StrategyPass)

	pool := client.NewPool()

	message := make(chan redis.Message, 1)

	// go client.Listen(pool, ProxySubChannel, message)
	go client.ListenChannel(pool, ProxySubChannel, message)

	defer func() {
		if err := recover(); err != nil {
			logger.Error("proxy panic recover: %v", err)
		}

	}()

	for {
		select {
		case msg := <-message:
			strData := string(msg.Data)
			logger.Info("accept %s message: %s", msg.Channel, strData)

			data, err := base64.StdEncoding.DecodeString(strData)
			if err != nil {
				logger.Error("decode error:", err)
				continue
			}

			var proxyStrategy comm.ProxyStrategy

			err = json.Unmarshal(data, &proxyStrategy)

			if err != nil {
				logger.Error(err)
			}
			proxyStrategy.Date = time.Now().Format(TimeFormat)
			agentId := comm.QueryEngineId()
			if agentId == proxyStrategy.AgentId {
				handleProxyAndCallback(proxyStrategy, client, pool)
			}
		}
	}
}

func zombieProcessClean() {

	for {

		time.Sleep(time.Second * 200)

		ok, proxyTable := comm.ReadProxyTable()

		if !ok {
			logger.Error("read file proxy-table error")
			continue
		}

		for _, proxyStrategy := range proxyTable {
			if comm.IsZombie(proxyStrategy.Pid) {
				pp, err := os.FindProcess(proxyStrategy.Pid)
				if err == nil && pp != nil {
					err = comm.KillProcess(pp)
					if err != nil {
						logger.Error("signal %v error", proxyStrategy.Pid, err)
						break
					}
				}
			}
		}
	}
}

func ipSelect(proxyStrategy comm.ProxyStrategy) string {
	if AgentConf.HoneyPublicIp != "" && comm.IsIpConnect(AgentConf.HoneyPublicIp) {
		return AgentConf.HoneyPublicIp // 透明代理一般只代理到蜜网
	}
	if strings.Index(proxyStrategy.HoneyIP, ",") > -1 {
		ipList := strings.Split(proxyStrategy.HoneyIP, ",")
		for _, ip := range ipList {
			if comm.IsIpConnect(ip) {
				return ip
			}
		}
	} else {
		if comm.IsIpConnect(proxyStrategy.HoneyIP) {
			return proxyStrategy.HoneyIP // honeyIp 可用不用替换
		}
	}

	return proxyStrategy.HoneyIP
}

func handleProxyAndCallback(proxyStrategy comm.ProxyStrategy, redisClient *comm.RedisServer, pool *redis.Pool) {

	ok1, proxyTable := comm.ReadProxyTable()
	ok2, processTable := comm.ReadProcessTable()

	if !ok1 || !ok2 {
		logger.Error("read proxy-table [%v] process-table [%v], quit handle", ok1, ok2)
		return
	}
	proxyCheckOk := checkProxyStrategy(proxyStrategy)
	if !proxyCheckOk {
		logger.Error("proxy port: %d occupied, or to big abort!", proxyStrategy.ListenPort)
		proxyStrategy.Status = -1 // 0 -> 出错
		result, err := json.Marshal(proxyStrategy)
		if err != nil {
			logger.Error("marshal proxy strategy err %v, %v", err, result)
		}
		encodedData := base64.StdEncoding.EncodeToString(result)
		redisClient.PublishMsg(pool, ProxyPubChannel, encodedData)
		logger.Info("publish message result: %s", result)
		return
	}

	proxyStrategy.HoneyIP = ipSelect(proxyStrategy)

	if strings.Index(proxyStrategy.HoneyIP, ",") > -1 {
		ips := strings.Split(proxyStrategy.HoneyIP, ",")
		logger.Warn("ips: [%v], chose first ip: %s", ips, ips[0])
		proxyStrategy.HoneyIP = ips[0]
	}

	key := fmt.Sprintf("%s-%d", proxyStrategy.ServerType, proxyStrategy.ListenPort)
	logger.Info("proxy-key[%s]", key)
	logger.Info("HoneyIP[%s]", proxyStrategy.HoneyIP)
	defer func() {
		if proxyStrategy.Status == 1 {
			logger.Info("proxy [%v] exec success", proxyStrategy)
		} else if proxyStrategy.Status == -1 {
			logger.Info("proxy [%v] exec fail", proxyStrategy)
		}
		_, proxyTable1 := comm.ReadProxyTable()
		logger.Info("----------------------------------------------------------------------")
		logger.Info(proxyTable1)
		logger.Info("----------------------------------------------------------------------")
	}()

	// TODO 可以存在多个名称相同的 进程  所以 Kye = ServerType + port
	queryPid, ok := processTable[key]

	if ok {
		err := queryPid.KillSelf()
		if err != nil {
			logger.Info("kill process: %s err: %v", queryPid, err)
		}
	}

	if proxyStrategy.Type == "UN_EDGE" || proxyStrategy.Type == "UN_RELAY" {
		logger.Info("start kill proxy [%s] pid [%d]", proxyStrategy.ServerType, proxyStrategy.Pid)
		queryPid, ok := processTable[key]
		if ok {
			err := queryPid.KillSelf()
			if err != nil {
				logger.Info("kill process: %s err: %v", queryPid, err)
				proxyStrategy.Status = -1
			}
		}
		delete(processTable, key)
		delete(proxyTable, key)
		logger.Info(" proxy [%s] pid [%d] kill success! ", proxyStrategy.ServerType, proxyStrategy.Pid)
		proxyStrategy.Status = 1
	} else if proxyStrategy.Status == 1 {
		var startedProxyPid comm.Pid
		var err error
		var startCmd string
		var startMode string
		if proxyStrategy.Type == "EDGE" { // 起边缘代理
			startCmd = AgentHome + Proxy
		} else if proxyStrategy.Type == "RELAY" { // 起中继代理
			startCmd = fmt.Sprintf("%s -backend %s:%d -bind :%d -seccenter %s", proxyStrategy.Path, proxyStrategy.HoneyIP, proxyStrategy.HoneyPort, proxyStrategy.ListenPort, proxyStrategy.SecCenter)
			startMode = "bash"
		}

		if proxyStrategy.Type == "RELAY" && comm.Exists(proxyStrategy.Path) == false {
			err = errors.New("file not exist")
		} else {
			startedProxyPid, err = comm.StartProject(startCmd, startMode, key)
		}
		logger.Info(startedProxyPid)
		if err != nil {
			logger.Error("start proxy err: %v", err)
			proxyStrategy.Status = -1 // 0 -> 出错
		} else {
			proxyStrategy.Pid = startedProxyPid.Id
			proxyStrategy.Status = 1 // 1 -> 成功
		}
		proxyTable[key] = proxyStrategy
	} else {
		logger.Warn("unknown type of proxy strategy [%v]", proxyStrategy)
	}
	logger.Info("current proxy-table: %v", proxyTable)
	comm.FlushData2File(proxyTable, ProxyTablePath)
	result, err := json.Marshal(proxyStrategy)
	if err != nil {
		logger.Error("marshal proxy strategy err %v, %v", err, result)
	}

	encodedData := base64.StdEncoding.EncodeToString(result)

	redisClient.PublishMsg(pool, ProxyPubChannel, encodedData)

	logger.Info("publish message result: %s", result)
}

func checkProxyStrategy(proxyStrategy comm.ProxyStrategy) bool {

	if proxyStrategy.Type == "EDGE" || proxyStrategy.Type == "RELAY" {
		logger.Info("new proxy try listen port : %v", proxyStrategy.ListenPort)
		if proxyStrategy.ListenPort > 65535 {
			logger.Error("Too large port %d, abort!", proxyStrategy.ListenPort)
		}
		portOccupied := comm.CheckPort(fmt.Sprintf("%d", proxyStrategy.ListenPort))
		if portOccupied {
			logger.Error("proxy port: %d occupied!", proxyStrategy.ListenPort)
			return false
		}
	}

	return true

}
