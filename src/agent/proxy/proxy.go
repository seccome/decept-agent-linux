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
	ProxySubChannel  = comm.AgentTaskRequestChannel
	ProxyPubChannel  = comm.AgentTaskResponseChannel
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
	agentId := comm.QueryEngineId()
	if err != nil {
		// TODO 文件夹错误或读取异常
		logger.Error("logger setting err: ", err)
	}
	AgentConf = comm.LoadAgentConf(AgentHome)

	//comm.ReadProxyTable() // 触发proxy-table.json 创建

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

			var taskPayload comm.TaskPayload

			err = json.Unmarshal(data, &taskPayload)

			if err != nil {
				logger.Error(err)
			}

			if taskPayload.AgentID != agentId {
				continue
			}

			var allProxyTaskPayload comm.AllProxyStrategy

			err = json.Unmarshal(data, &allProxyTaskPayload)

			if err != nil {
				logger.Error(err)
			}
			if taskPayload.TaskType == comm.ProtocolProxy {

				handleProtocolProxy(allProxyTaskPayload, client, pool)
			} else if taskPayload.TaskType == comm.TransparentProxy {

				handleTransparentProxy(allProxyTaskPayload, client, pool)
			}
		}
	}
}

func zombieProcessClean() {

	for {

		time.Sleep(time.Second * 200)

		ok, proxyTable := comm.ReadAllProxyTable()

		if !ok {
			logger.Error("read file proxy-table error")
			continue
		}

		for _, proxyStrategy := range proxyTable {
			if comm.IsZombie(proxyStrategy.ProcessId) {
				pp, err := os.FindProcess(proxyStrategy.ProcessId)
				if err == nil && pp != nil {
					err = comm.KillProcess(pp)
					if err != nil {
						logger.Error("signal %v error", proxyStrategy.ProcessId, err)
						break
					}
				}
			}
		}
	}
}

func ipSelect(ips string) string {
	if AgentConf.HoneyPublicIp != "" && comm.IsIpConnect(AgentConf.HoneyPublicIp) {
		return AgentConf.HoneyPublicIp // 透明代理一般只代理到蜜网
	}
	if strings.Index(ips, ",") > -1 {
		ipList := strings.Split(ips, ",")
		for _, ip := range ipList {
			if comm.IsIpConnect(ip) {
				return ip
			}
		}
	}
	comm.IsIpConnect(ips)
	return ips
}

func handleProtocolProxy(protocolProxyTaskPayload comm.AllProxyStrategy, redisClient *comm.RedisServer, pool *redis.Pool) {

	ok1, proxyTable := comm.ReadAllProxyTable()
	ok2, processTable := comm.ReadProcessTable()

	if !ok1 || !ok2 {
		logger.Error("read proxy-table [%v] process-table [%v], quit handle", ok1, ok2)
		return
	}

	if protocolProxyTaskPayload.OperatorType == comm.DEPLOY && !portCheck(protocolProxyTaskPayload.ProxyPort) {
		logger.Error("proxy port: %d occupied, or to big abort!", protocolProxyTaskPayload.ProxyPort)
		protocolProxyTaskPayload.Status = comm.FAILED
		result, err := json.Marshal(protocolProxyTaskPayload)
		if err != nil {
			logger.Error("marshal proxy strategy err %v, %v", err, result)
		}
		encodedData := base64.StdEncoding.EncodeToString(result)
		redisClient.PublishMsg(pool, ProxyPubChannel, encodedData)
		logger.Info("publish message result: %s", result)
		return
	}

	key := fmt.Sprintf("%s-%d", protocolProxyTaskPayload.ProxyType, protocolProxyTaskPayload.ProxyPort)
	logger.Info("proxy-key[%s] proxy for honeypot [%s:%d]", key, protocolProxyTaskPayload.HoneypotIP, protocolProxyTaskPayload.HoneypotPort)
	defer func() {
		if protocolProxyTaskPayload.Status == comm.SUCCESS {
			logger.Info("proxy [%v] exec success", protocolProxyTaskPayload)
		} else if protocolProxyTaskPayload.Status == comm.FAILED {
			logger.Info("proxy [%v] exec fail", protocolProxyTaskPayload)
		}
	}()

	if protocolProxyTaskPayload.OperatorType == comm.WITHDRAW {
		logger.Info("start kill proxy [%s] pid [%d]", protocolProxyTaskPayload.ProxyType, protocolProxyTaskPayload.ProcessId)
		queryPid, ok := processTable[key]
		if ok {
			err := queryPid.KillSelf()
			if err != nil {
				logger.Info("kill process: %s err: %v", queryPid, err)
				protocolProxyTaskPayload.Status = comm.FAILED
			}
		}
		delete(processTable, key)
		delete(proxyTable, key)
		logger.Info("proxy [%s] pid [%d] kill success! ", protocolProxyTaskPayload.ProxyType, protocolProxyTaskPayload.ProcessId)
		protocolProxyTaskPayload.Status = comm.SUCCESS
	} else if protocolProxyTaskPayload.OperatorType == comm.DEPLOY {
		var startedProxyPid comm.Pid
		var err error
		var startCmd string
		var startMode string
		startCmd = fmt.Sprintf("%s -backend %s:%d -bind :%d -seccenter %s", protocolProxyTaskPayload.DeployPath, protocolProxyTaskPayload.HoneypotIP, protocolProxyTaskPayload.HoneypotPort, protocolProxyTaskPayload.ProxyPort, protocolProxyTaskPayload.SecCenter)
		startMode = "bash"

		if comm.Exists(protocolProxyTaskPayload.DeployPath) == false {
			err = errors.New("file not exist")
		} else {
			startedProxyPid, err = comm.StartProject(startCmd, startMode, key)
			logger.Info(startedProxyPid)
		}
		if err != nil {
			logger.Error("start proxy err: %v", err)
			protocolProxyTaskPayload.Status = comm.FAILED // 0 -> 出错
		} else {
			protocolProxyTaskPayload.ProcessId = startedProxyPid.Id
			protocolProxyTaskPayload.Status = comm.SUCCESS // 0 -> 出错
		}
		proxyTable[key] = protocolProxyTaskPayload
	} else {
		logger.Warn("unknown type of proxy strategy [%v]", protocolProxyTaskPayload)
	}
	logger.Info("current proxy-table: %v", proxyTable)
	comm.FlushData2File(proxyTable, ProxyTablePath)
	result, err := json.Marshal(protocolProxyTaskPayload)
	if err != nil {
		logger.Error("marshal proxy strategy err %v, %v", err, result)
	}
	encodedData := base64.StdEncoding.EncodeToString(result)

	redisClient.PublishMsg(pool, ProxyPubChannel, encodedData)

	logger.Info("publish message result: %s", result)
}

func handleTransparentProxy(transparentProxy comm.AllProxyStrategy, redisClient *comm.RedisServer, pool *redis.Pool) {

	ok1, proxyTable := comm.ReadAllProxyTable()
	ok2, processTable := comm.ReadProcessTable()

	if !ok1 || !ok2 {
		logger.Error("read proxy-table [%v] process-table [%v], quit handle", ok1, ok2)
		return
	}

	proxyCheckOk := portCheck(transparentProxy.ProxyPort)
	if !proxyCheckOk {
		logger.Error("proxy port: %d occupied, or to big abort!", transparentProxy.ProxyPort)
		transparentProxy.Status = comm.FAILED // 0 -> 出错
		result, err := json.Marshal(transparentProxy)
		if err != nil {
			logger.Error("marshal proxy strategy err %v, %v", err, result)
		}
		encodedData := base64.StdEncoding.EncodeToString(result)
		redisClient.PublishMsg(pool, ProxyPubChannel, encodedData)
		logger.Info("publish message result: %s", result)
		return
	}

	transparentProxy.HoneypotServerIP = ipSelect(transparentProxy.HoneypotServerIP)

	if strings.Index(transparentProxy.HoneypotServerIP, ",") > -1 {
		ips := strings.Split(transparentProxy.HoneypotServerIP, ",")
		logger.Warn("ips: [%v], chose first ip: %s", ips, ips[0])
		transparentProxy.HoneypotServerIP = ips[0]
	}

	key := fmt.Sprintf("%s-%d", transparentProxy.ProxyType, transparentProxy.ProxyPort)
	logger.Info("start transparent proxy [%s]", key)
	defer func() {
		if transparentProxy.Status == comm.SUCCESS {
			logger.Info("proxy [%v] exec success", transparentProxy)
		} else if transparentProxy.Status == comm.FAILED {
			logger.Info("proxy [%v] exec fail", transparentProxy)
		}
		_, proxyTable1 := comm.ReadAllProxyTable()
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

	if transparentProxy.OperatorType == comm.WITHDRAW {
		logger.Info("start kill proxy [%s] pid [%d]", key, transparentProxy.ProcessId)
		queryPid, ok := processTable[key]
		if ok {
			err := queryPid.KillSelf()
			if err != nil {
				logger.Info("kill process: %s err: %v", queryPid, err)
				transparentProxy.Status = comm.FAILED
			}
		}
		delete(processTable, key)
		delete(proxyTable, key)
		logger.Info(" proxy [%s] pid [%d] kill success! ", transparentProxy.ProxyType, transparentProxy.ProcessId)
		transparentProxy.Status = comm.SUCCESS
	} else if transparentProxy.OperatorType == comm.DEPLOY {
		var startedProxyPid comm.Pid
		var err error
		var startCmd string
		startCmd = AgentHome + Proxy
		startedProxyPid, err = comm.StartProject(startCmd, "", key)
		logger.Info(startedProxyPid)
		if err != nil {
			logger.Error("start proxy err: %v", err)
			transparentProxy.Status = comm.FAILED // 0 -> 出错
		} else {
			transparentProxy.ProcessId = startedProxyPid.Id
			transparentProxy.Status = comm.SUCCESS // 1 -> 成功
		}
		proxyTable[key] = transparentProxy
	} else {
		logger.Warn("unknown type of transparent proxy strategy [%v]", transparentProxy)
	}
	logger.Info("current proxy-table: %v", proxyTable)
	comm.FlushData2File(proxyTable, ProxyTablePath)
	result, err := json.Marshal(transparentProxy)
	if err != nil {
		logger.Error("marshal proxy strategy err %v, %v", err, result)
	}

	encodedData := base64.StdEncoding.EncodeToString(result)

	redisClient.PublishMsg(pool, ProxyPubChannel, encodedData)

	logger.Info("publish message result: %s", result)
}

func portCheck(port int32) bool {

	if port > 65535 {
		logger.Error("Too large port %d, abort!", port)
	}
	portOccupied := comm.CheckPort(fmt.Sprintf("%d", port))
	if portOccupied {
		logger.Error("proxy port: %d occupied!", port)
		return false
	}

	return true

}
