package comm

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"os"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"util/logger"
	"util/zero"
)

const (
	HEARTBEAT            = 1000
	POLICY               = 1001
	ATTACK               = 1002
	MONITOR              = 1003
	ASSET                = 1004
	REBOUND_SHELL_ATTACK = 1005
)

const (
	BruteForce          = 1
	FILE_Monit          = 2
	Command_Monit       = 3
	Process_Monit       = 4
	Network_Monit       = 5
	WebShell            = 6
	BaseLine            = 7
	AppLog              = 8
	Audit               = 9
	Asset               = 10
	Rebound_Shell_Event = 11
)

type Plugin struct {
	Modname    string `json:"modname"`
	Enable     string `json:"enable"`
	Updatetime string `json:"updatetime"`
	Data       string `json:"data"`
}

type Policy struct {
	Agentid  string   `json:"agentid"`
	Policyid string   `json:"policyid"`
	Md5sum   string   `json:"md5sum"`
	Plugins  []Plugin `json:"plugins"`
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

func DeepFields(ifaceType reflect.Type) []reflect.StructField {
	var fields []reflect.StructField

	for i := 0; i < ifaceType.NumField(); i++ {
		v := ifaceType.Field(i)
		if v.Anonymous && v.Type.Kind() == reflect.Struct {
			fields = append(fields, DeepFields(v.Type)...)
		} else {
			fields = append(fields, v)
		}
	}

	return fields
}

func StructCopy(DstStructPtr interface{}, SrcStructPtr interface{}) {
	srcv := reflect.ValueOf(SrcStructPtr)
	dstv := reflect.ValueOf(DstStructPtr)
	srct := reflect.TypeOf(SrcStructPtr)
	dstt := reflect.TypeOf(DstStructPtr)
	if srct.Kind() != reflect.Ptr || dstt.Kind() != reflect.Ptr ||
		srct.Elem().Kind() == reflect.Ptr || dstt.Elem().Kind() == reflect.Ptr {
		panic("Fatal error:type of parameters must be Ptr of value")
	}
	if srcv.IsNil() || dstv.IsNil() {
		panic("Fatal error:value of parameters should not be nil")
	}
	srcV := srcv.Elem()
	dstV := dstv.Elem()
	srcfields := DeepFields(reflect.ValueOf(SrcStructPtr).Elem().Type())
	for _, v := range srcfields {
		if v.Anonymous {
			continue
		}
		dst := dstV.FieldByName(v.Name)
		src := srcV.FieldByName(v.Name)
		if !dst.IsValid() {
			continue
		}
		if src.Type() == dst.Type() && dst.CanSet() {
			dst.Set(src)
			continue
		}
		if src.Kind() == reflect.Ptr && !src.IsNil() && src.Type().Elem() == dst.Type() {
			dst.Set(src.Elem())
			continue
		}
		if dst.Kind() == reflect.Ptr && dst.Type().Elem() == src.Type() {
			dst.Set(reflect.New(src.Type()))
			dst.Elem().Set(src)
			continue
		}
	}
	return
}

func CopyString(s string) string {
	return string([]byte(s))
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

func ReadProxyTable() (bool, map[string]ProxyStrategy) {
	var proxyTable map[string]ProxyStrategy
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

func Daemon(nochdir, noclose int) int {
	var ret, ret2 uintptr
	var err syscall.Errno

	darwin := runtime.GOOS == "darwin"

	// already a daemon
	if syscall.Getppid() == 1 {
		return 0
	}

	// fork off the parent process
	ret, ret2, err = syscall.RawSyscall(syscall.SYS_FORK, 0, 0, 0)
	if err != 0 {
		return -1
	}

	// failure
	if ret2 < 0 {
		os.Exit(-1)
	}

	// handle exception for darwin
	if darwin && ret2 == 1 {
		ret = 0
	}

	// if we got a good PID, then we call exit the parent process.
	if ret > 0 {
		os.Exit(0)
	}

	/* Change the file mode mask */
	_ = syscall.Umask(0)

	// create a new SID for the child process
	s_ret, s_errno := syscall.Setsid()
	if s_errno != nil {
		log.Printf("Error: syscall.Setsid errno: %d", s_errno)
	}
	if s_ret < 0 {
		return -1
	}

	if nochdir == 0 {
		os.Chdir("/")
	}

	if noclose == 0 {
		f, e := os.OpenFile("/dev/null", os.O_RDWR, 0)
		if e == nil {
			fd := f.Fd()
			syscall.Dup2(int(fd), int(os.Stdin.Fd()))
			syscall.Dup2(int(fd), int(os.Stdout.Fd()))
			syscall.Dup2(int(fd), int(os.Stderr.Fd()))
		}
	}

	return 0
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

type ProxyStrategy struct {
	BasePolicy

	ListenPort int
	ServerType string
	HoneyIP    string
	HoneyPort  int
	Pid        int    // hosteye agent 自有
	Path       string // 中继代理才有
	Date       string
	SecCenter  string
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
