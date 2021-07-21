package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/go-redis/redis"
	"github.com/shirou/gopsutil/process"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
	"util/comm"
	"util/logger"
)

var (
	AgentHomeDir      = comm.AgentHome()
	ProxyStrategy     comm.ProxyStrategy
	ProxyEventChannel = "proxy-event-pub-channel"
)

func main() {
	/*
		1. 设置日志 2. 解析配置 3. 启动代理
	*/
	var (
		ProxyLogConfPath = fmt.Sprintf("%s%s", comm.AgentHome(), "/proxy/conf/log.json")
	)

	err := logger.SetLogger(ProxyLogConfPath)

	if err != nil {
		// TODO 文件夹错误或读取异常
		logger.Error("logger setting err: ", err)
	}

	thisPid := os.Getpid()
	p, err := process.NewProcess(int32(thisPid))
	if err != nil {
		logger.Fatal(err)
	}

	ok, proxyTable := comm.ReadProxyTable()

	if !ok {
		logger.Error("read file proxy-table error")
		return
	}

	logger.Info(proxyTable)

	cmdLine, err := p.Cmdline()
	if err != nil {
		logger.Error(err)
	}

	cmdArray := strings.Split(cmdLine, " ")

	logger.Info(cmdArray)

	var proxyName string

	for key, pid := range proxyTable {
		if pid.Pid == thisPid {
			ProxyStrategy = pid
			proxyName = key
		}
	}

	logger.Info("current proxy key: %s, pid: %v cmdLine: %s", proxyName, p, cmdLine)

	if proxyName == "" {
		logger.Error("proxy configuration not found")
		return
	}

	flag.Parse()

	bind := fmt.Sprintf("0.0.0.0:%d", ProxyStrategy.ListenPort)
	logger.Info("proxy addr %s", bind)

	portBind := comm.CheckPort(fmt.Sprintf("%d", ProxyStrategy.ListenPort))
	if portBind {
		logger.Error("port: %d occupied, abort!", ProxyStrategy.ListenPort)
		log.Fatalf("port: %d occupied", ProxyStrategy.ListenPort)
	}

	backend := fmt.Sprintf("%s:%d", ProxyStrategy.HoneyIP, ProxyStrategy.HoneyPort)
	logger.Info("honey addr %s", backend)

	ln, err := net.Listen("tcp", bind)
	if err != nil {
		logger.Error("listening error: %v", err)
		log.Fatalf("listening: %v", err)
	}
	logger.Info("start proxying [%s]", backend)

	go comm.MonitForKillSelfTask()
	go comm.StartMemCpuMonitor(proxyName, 5)

	err = proxy(ln, backend)

	if err != nil {
		logger.Info("proxy: %v", err)
		log.Fatalf("proxy: %v", err)
	}
}

func proxy(ln net.Listener, backend string) error {
	for {
		conn, err := ln.Accept()
		if err != nil {
			logger.Error("proxy start error: %v, exist!", err)
			return err
		}
		go handle(conn, backend)
	}
}

func reportConnectEvent(conn net.Conn, removeCon net.Conn) {
	remoteParts := strings.Split(removeCon.LocalAddr().String(), ":")
	ePort, _ := strconv.Atoi(remoteParts[1])
	connectEvent := ConnectEvent{
		AgentId:    comm.QueryEngineId(),
		SourceAddr: conn.RemoteAddr().String(),
		BindPort:   ProxyStrategy.ListenPort,
		DestAddr:   ProxyStrategy.HoneyIP,
		DestPort:   ProxyStrategy.HoneyPort,
		EventTime:  time.Now().Unix(),
		ProxyType:  "EDGE",
		ExportPort: ePort,
	}
	result, err := json.Marshal(connectEvent)
	logger.Info(string(result))
	if err != nil {
		logger.Error("marshal proxy strategy err %v, %v", err, result)
	}
	encodedData := base64.StdEncoding.EncodeToString(result)

	agentConf := comm.LoadAgentConf(AgentHomeDir)

	logger.Info(agentConf.StrategyAddr)
	logger.Info(agentConf.StrategyPass)

	client := redis.NewClient(&redis.Options{
		Addr:     agentConf.StrategyAddr,
		Password: agentConf.StrategyPass,
		DB:       0,
	})

	_, err = client.Ping().Result()

	if err != nil {
		fmt.Errorf("redis connect err %v", err)
	}

	defer client.Close()
	intCmd2 := client.Publish(ProxyEventChannel, encodedData)
	logger.Info("%s publish message result: %s", ProxyEventChannel, intCmd2)

}
func handle(localConn net.Conn, backend string) {
	defer localConn.Close()
	remoteConn, err := net.Dial("tcp", backend)
	if err != nil {
		logger.Info("dialing remote: %v", err)
		return
	}
	logger.Info("connected %s to %s", localConn, remoteConn)
	reportConnectEvent(localConn, remoteConn)
	defer remoteConn.Close()

	copy(localConn, remoteConn)
}

type ConnectEvent struct {
	AgentId    string
	SourceAddr string
	BindPort   int
	ExportPort int
	DestAddr   string
	DestPort   int
	EventTime  int64
	ProxyType  string
}

func copy(localConn, remoteConn io.ReadWriter) {
	done := make(chan struct{})
	go func() {
		io.Copy(localConn, remoteConn)
		done <- struct{}{}
	}()
	go func() {
		io.Copy(remoteConn, localConn)
		done <- struct{}{}
	}()
	<-done
	<-done
}
