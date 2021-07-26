package comm

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
	"util/logger"
	"util/zero"
)

const (
	AgentTaskRequestChannel  = "agent-task-request-channel"
	AgentTaskResponseChannel = "agent-task-response-channel"
)

//任务类型
type TaskType string

type BaitType string

type TaskStatus int32

type OperatorType string

const (
	PROTOCOL         TaskType = "PROTOCOL"          //协议
	SIGNATURE        TaskType = "SIGNATURE"         //密签
	BAIT             TaskType = "BAIT"              //诱饵
	ProtocolProxy    TaskType = "PROTOCOL_PROXY"    //协议代理
	TransparentProxy TaskType = "TRANSPARENT_PROXY" //透明代理
	Heartbeat        TaskType = "HEARTBEAT"         //心跳
)

const (
	FILE_BAIT BaitType = "FILE_BAIT" //协议
	HIS_BAIT  BaitType = "HIS_BAIT"  //密签

)

//公共状态定义
const (
	IDLE    TaskStatus = -1 //初始
	RUNNING TaskStatus = 1  //下发中
	FAILED  TaskStatus = 2  //异常
	SUCCESS TaskStatus = 3  //成功
)

const (
	DEPLOY   OperatorType = "DEPLOY"   //部署
	WITHDRAW OperatorType = "WITHDRAW" //撤回
)

type TaskPayload struct {
	TaskID       string       `json:"TaskID"`
	Status       TaskStatus   `json:"Status"`
	AgentID      string       `json:"AgentID"`
	TaskType     TaskType     `json:"TaskType"`
	OperatorType OperatorType `json:"OperatorType"`
}

//协议部署
type FileDeployTaskPayload struct {
	TaskPayload
	FileMD5           string            `json:"FileMD5"`
	CommandParameters map[string]string `json:"CommandParameters"`
	URL               string            `json:"URL"`
}

type BaitStrategy struct {
	TaskPayload
	BaitType BaitType `json:"BaitType"`
}

//诱饵密签文件部署 撤回
type FileBaitDeployTaskPayload struct {
	BaitStrategy
	FileMD5           string            `json:"FileMD5"`
	CommandParameters map[string]string `json:"CommandParameters"`
	URL               string            `json:"URL"`
}

//history诱饵部署
type HistoryBaitDeployTaskPayload struct {
	BaitStrategy
	BashHistoryPath string `json:"BashHistoryPath"`
	RandomLine      string `json:"RandomLine"`
	HisBaitItem     []HisBaitItem
}

//协议代理部署 撤回  TODO 要加类型ProxyType  http/telnet..
type ProtocolProxyTaskPayload struct {
	ProxyStrategy
	HoneypotPort int32  `json:"HoneypotPort"`
	HoneypotIP   string `json:"HoneypotIP"`
	DeployPath   string `json:"DeployPath"`
	SecCenter    string `json:"SecCenter"`
}

type ProxyStrategy struct {
	TaskPayload
	ProxyPort int32  `json:"ProxyPort"`
	ProxyType string `json:"ProxyType"`
	ProcessId int
}

type AllProxyStrategy struct {
	ProxyStrategy
	HoneypotServerPort int32  `json:"HoneypotServerPort"`
	HoneypotServerIP   string `json:"HoneypotServerIP"`
	ProbeIP            string `json:"ProbeIP"`
	HoneypotPort       int32  `json:"HoneypotPort"`
	HoneypotIP         string `json:"HoneypotIP"`
	DeployPath         string `json:"DeployPath"`
	SecCenter          string `json:"SecCenter"`
}

//透明代理部署 撤回
type TransparentProxyTaskPayload struct {
	ProxyStrategy
	HoneypotServerPort int32  `json:"HoneypotServerPort"`
	HoneypotServerIP   string `json:"HoneypotServerIP"`
	ProbeIP            string `json:"ProbeIP"`
}

type ProtocolProxyStrategy struct {
	TaskID       string `json:"TaskID"`
	Status       int32  `json:"Status"`
	AgentID      string `json:"AgentID"`
	TaskType     string `json:"TaskType"`
	OperatorType string `json:"OperatorType"`
	ProxyPort    int32  `json:"ProxyPort"`
	HoneypotPort int32  `json:"HoneypotPort"`
	HoneypotIP   string `json:"HoneypotIP"`
	DeployPath   string `json:"DeployPath"`
}

// 透明代理连接事件
type ConnectEvent struct {
	AgentId    string
	SourceAddr string
	BindPort   int32
	ExportPort int
	DestAddr   string
	DestPort   int32
	EventTime  int64
	ProxyType  string
}

//go get golang.org/x/sys/unix

type FileConf struct {
	Debug      bool   `json:"debug"`
	CertFile   string `json:"certFile"`
	KeyFile    string `json:"keyFile"`
	CaFile     string `json:"caFile"`
	AuthAddr   string `json:"authAddr"`
	CommAddr   string `json:"commAddr"`
	PolicyAddr string `json:"policyAddr"`
	PolicyPass string `json:"policyPass"`
}

type AgentConf struct {
	StrategyAddr     string `json:"strategyAddr"`
	StrategyPass     string `json:"strategyPass"`
	Version          string `json:"version"`
	HeartbeatChannel string `json:"heartbeatChannel"`
	SshKeyUploadUrl  string `json:"sshKeyUploadUrl"`
	HoneyPublicIp    string `json:"honeyPublicIp"`
}

type SshKeyBody struct {
	SshKey  string `json:"ssh_key"`
	AgentId string `json:"agentid"`
}

func AgentHome() string {
	AgentHome, _ := os.Getwd()
	return AgentHome
}

func LoadAgentConf(agentHome string) AgentConf {
	agentConfPath := agentHome + "/conf/agent.json"
	var conf AgentConf
	itf, ok := LoadFileForObj(agentConfPath, conf)
	if !ok {
		logger.Error("load ./conf/agent.json fail")
		return conf
	}
	conf = itf.(AgentConf)
	return conf
}

func LoadFileForObj(filename string, itf interface{}) (interface{}, bool) {
	switch itf.(type) {
	case AgentConf:
		var conf AgentConf
		fileObj, err := os.Open(filename)
		if err != nil {
			return nil, false
		}

		defer fileObj.Close()

		contents, err := ioutil.ReadAll(fileObj)
		if err != nil {
			return nil, false
		}
		result := strings.Replace(string(contents), "\n", "", 1)
		err = json.Unmarshal([]byte(result), &conf)
		if err != nil {
			logger.Error("read file fail(%s)", result)
			return conf, false
		}
		return conf, true
	default:
		logger.Warn("type unknown")
	}

	return nil, false
}

var (
	lock       sync.Mutex
	isInit     bool
	msgsToSend = make(chan Msg, 100)
)

func ReadFile(filePath string) (bool, string) {
	file, err := os.Open(filePath) //只读打开
	if err != nil {
		fmt.Println("open file error: ", err)
		return false, ""
	}
	defer file.Close() //关闭文件
	context := make([]byte, 10000)
	readCount := 0
	readTime := 0
	for {
		readTime++
		readNum, err := file.Read(context)

		if err != nil && err != io.EOF {
			logger.Error(err) //有错误抛出异常
		}
		readCount = readCount + readNum
		if 0 == readNum || readTime > 100 {
			break //当读取完毕时候退出循环
		}
	}
	return true, string(context[0:readCount])
}

func ReadAllProxyTable() (bool, map[string]AllProxyStrategy) {
	var proxyTable map[string]AllProxyStrategy
	proxyTablePath := AgentHome() + "/proxy/proxy-table.json"
	if Exists(proxyTablePath) == false {
		logger.Info("file %s not exists, create!", proxyTablePath)
		var file *os.File
		file, err := os.Create(proxyTablePath)
		if err != nil {
			logger.Error(err)
			return false, proxyTable
		}
		defer file.Close()
		_, err = io.WriteString(file, "{}")
		if err != nil {
			logger.Error("write string to file[%s] err: %v", proxyTablePath, err)
			return false, proxyTable
		}
	}

	file, err := os.Open("proxy/proxy-table.json") //只读打开
	if err != nil {
		fmt.Println("open file error: ", err)
		return false, proxyTable
	}
	defer file.Close() //关闭文件
	context := make([]byte, 10000)
	readCount := 0
	for {
		readNum, err := file.Read(context)
		if err != nil && err != io.EOF {
			logger.Error(err) //有错误抛出异常
		}
		readCount = readCount + readNum
		if 0 == readNum {
			break //当读取完毕时候退出循环
		}
	}

	result := string(context[0:readCount])
	err = json.Unmarshal([]byte(result), &proxyTable)
	if err != nil {
		logger.Error(err)
		logger.Info("result: %s", result)
		return false, proxyTable
	}

	return true, proxyTable
}

func QueryEngineId() string {
	contents, err := ioutil.ReadFile("conf/agent")
	if err != nil {
		logger.Error(err)
		return ""
	}
	return strings.Replace(string(contents), "\n", "", 1)
}

func clearProcessTable(name string) {
	success, processTable := ReadProcessTable()

	if !success {
		logger.Error("read process table fail")
		return
	}

	if _, ok := processTable[name]; ok {
		delete(processTable, name)
	}

	FlushData2File(processTable, ProcessTablePath)
}

/**
先主动Kill 然后标记Kill(清除Process-Table)
*/
func (p Pid) KillSelf() error {

	clearProcessTable(p.Name)

	if !ProcessIsExist(p.Id) {
		logger.Info("kill target process name [%s] pid [%d]  not existed", p.Name, p)
		return nil
	}

	pp, err := os.FindProcess(p.Id)

	if err == nil && pp != nil {
		err = KillProcess(pp)
		if err != nil {
			logger.Error("signal %v error", p.Id, err)
			return err
		}
	}

	if p.Name != "hosteye_engine" {
		ppParent, err := os.FindProcess(p.Id + 1)
		if err == nil && ppParent != nil && ProcessIsExist(p.Id+1) {
			err = KillProcess(ppParent)
			if err != nil {
				logger.Error("signal %v error", ppParent.Pid, err)
				return err
			}
		}
	}

	return nil
}

func KillProcess(pp *os.Process) error {
	err := pp.Signal(syscall.SIGKILL)
	if err != nil {
		logger.Error("signal %v error", pp.Pid, err)
		logger.Info("kill process [%d] fail", pp.Pid)
		return err
	}
	time.Sleep(time.Millisecond * 300)

	queryP, err := os.FindProcess(pp.Pid)

	if err == nil && queryP != nil {
		_, _ = pp.Wait()
	}
	logger.Info("kill process [%d] success", pp.Pid)
	return nil
}

type Msg struct {
	data []byte
	addr string
}

func SendMsg(msgID int32, evetmsg string, commAddr string) error {
	if !isInit {
		lock.Lock()
		if !isInit {
			go func() {
				for {
					mm := make([]Msg, 0)
					select {
					case msg := <-msgsToSend:
						mm = append(mm, msg)

						t := time.NewTicker(time.Second)
					DONE:
						for {
							select {
							case m := <-msgsToSend:
								mm = append(mm, m)
								if len(mm) > 100 {
									break DONE
								}
							case <-t.C:
								break DONE
							}
						}
						t.Stop()
					}

					go doSend(mm)
				}
			}()
			isInit = true
		}
		lock.Unlock()
	}

	msg := zero.NewMessage(msgID, []byte(evetmsg))
	data, err := zero.Encode(msg)
	if err != nil {
		return err
	}

	msgsToSend <- Msg{
		data: data,
		addr: commAddr,
	}
	return nil
}

type HeartBeat struct {
	AgentId  string
	Status   string // 表示当前agent 状态
	Version  string // 为之后升级
	IPs      string
	HostName string
	Type     string
}

func doSend(msg []Msg) {
	m := make(map[string][]*Msg)
	for _, v := range msg {
		_, exist := m[v.addr]
		if !exist {
			m[v.addr] = make([]*Msg, 0)
		}
		n := v
		m[v.addr] = append(m[v.addr], &n)
	}

	for addr, tm := range m {
		go func() {
			tcpAddr, err := net.ResolveTCPAddr("tcp", addr)
			if err != nil {
				return
			}
			conn, err := net.DialTCP("tcp", nil, tcpAddr)
			if err != nil {
				return
			}
			defer conn.Close()
			for _, v := range tm {
				_, err = conn.Write(v.data)
				if err != nil {
					return
				}
			}
			time.Sleep(10 * time.Second)
		}()
	}
}

type EngineStrategyResp struct {
	Code int
	Msg  string
	Data EngineStrategy
}

type EngineStrategy struct {
	LatestVersion string
	Md5           string
}

type Pid struct {
	Name   string
	Id     int
	Result string
	Mode   string
}

/*
	诱饵父类 提供类别、agentId/taskId/下发状态/执行状态 这些总体的描述数据
*/
type BasePolicy struct {
	TaskId  string // 任务ID
	AgentId string // 选择执行本策略的 Agent
	Status  int    // 下发端 1: OPEN | 0:CLOSE // 执行端 SUCCESS | FAIL
	Type    string // FILE | HIS
	Data    string // 诱饵压缩包下载地址 HTTP
}

/**
文件诱饵的策略数据结构
*/
type FileBaitPolicy struct {
	BasePolicy
	Md5 string // 压缩包MD5值
}

/**
History诱饵的策略数据结构
*/
type HisBaitPolicy struct {
	BashHistoryPath string
	RandomLine      string
	Enabled         bool
	Honeybits       []HisBaitItem
}

type HisBaitItem struct {
	Enabled         bool
	User            string
	Pass            string
	Addr            string
	Port            string
	Url             string
	RemotePath      string
	LocalPath       string
	Command         string
	Htype           string
	DbName          string
	Accesskeyid     string
	RegionId        string
	Secretaccesskey string
}
