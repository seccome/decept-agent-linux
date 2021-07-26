package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"github.com/gomodule/redigo/redis"
	_ "github.com/mkevac/debugcharts"
	"github.com/shirou/gopsutil/process"
	"io"
	"io/ioutil"
	"net"
	_ "net/http/pprof"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
	"util/comm"
	"util/logger"
	"util/uuid"
)

var (
	AgentHome        = comm.AgentHome()
	AgentConfPath    = AgentHome + "/conf/agent.json"
	AgentLogConfPath = AgentHome + "/conf/log.json"
	ProxyPath        = AgentHome + "/proxy/decept-proxy"
	BaitPath         = AgentHome + "/bait/decept-bait"
	ProxyName        = "decept-proxy"
	BaitName         = "decept-bait"
	AgentConf        comm.AgentConf
	PidTable         = make(map[string]comm.Pid)
	ProxyEnable      bool
	BaitEnable       bool
	HostScanEnable   bool
	StartMode        string
	SpecifyIP        string
	AgentHeartBeat   comm.HeartBeat
)

func main() {
	AgentHeartBeat.Status = "starting"
	prepare()
	go moduleLifecycleManagementTask()
	go processResourceOccupancyMonitoring()
	go comm.StartMemCpuMonitor("decept-agent", 5)
	AgentHeartBeat.Status = "running"
	AgentHeartBeat.Version = AgentConf.Version

	//go func() {
	//	http.ListenAndServe("0.0.0.0:8899", nil)
	//}()

	heartBeat()

}

// TODO 代理转发的CPU 限制 应该大于 5 不一致
func processResourceOccupancyMonitoring() {
	logger.Debug("start mem and cpu monitor job")

	for {
		time.Sleep(time.Second * 5)
		_, PidTable = comm.ReadProcessTable()
		update := false
		for name, pid := range PidTable {
			cpuLimit := 6
			processId := pid.Id
			if comm.ProcessIsExist(processId) && isMemOverloaded(name, processId, float64(cpuLimit)) {
				if pid, ok := PidTable[name]; ok {
					err := pid.KillSelf()
					if err != nil {
						logger.Error(err)
					}
				}
			}

			if !comm.ProcessIsExist(processId) {
				delete(PidTable, name)
				logger.Info("process name [%s] pid [%v] is dead clear it form process-table", name, pid)
				update = true
			}
		}
		if update {
			comm.UpdateProcessTable(PidTable)
		}
	}
}

func prepare() {

	flag.StringVar(&SpecifyIP, "ip", "127.0.0.1", "指定IP")
	flag.StringVar(&StartMode, "mode", "edge", "启动模式")
	flag.BoolVar(&ProxyEnable, "proxy", true, "proxy mou")
	flag.BoolVar(&BaitEnable, "bait", true, "seccenter")
	logger.Info("hosteye agent start path: %s", AgentHome)
	flag.Parse()
	err := logger.SetLogger(AgentLogConfPath)

	if err != nil {
		// TODO 文件夹错误或读取异常
		logger.Error("logger setting err: ", err)
	}

	logger.Info("hosteye agent start mode [%s] specify-ip", StartMode, SpecifyIP)

	AgentConf = comm.LoadAgentConf(AgentHome)

	if "clear" == StartMode {
		_, PidTable = comm.ReadProcessTable()
		for name, pid := range PidTable {
			if comm.ProcessIsExist(pid.Id) {
				logger.Info("the last generation process [%s] pid [%d] existing start kill", name, pid.Id)
				err = pid.KillSelf()
				if err != nil {
					logger.Error("kill [%s] err [%v]", name, err)
				} else {
					logger.Info("kill [%s] success, process existing [%v]", name, comm.ProcessIsExist(pid.Id))
				}
			}
		}
	}
	comm.ReadProcessTable() // 触发process-table.json 创建
	if "RELAY" == StartMode || "relay" == StartMode {
		go prepareAndUploadSshKey()
	}

}

func prepareAndUploadSshKey() {
	logger.Info("start create ssh key file")
	for {
		uploadResult := GenAndUploadRsaKey(2048)
		if uploadResult != nil {
			uploadResult = GenAndUploadRsaKey(2048)
			time.Sleep(time.Minute)
		} else {
			return
		}
	}
}

func GenAndUploadRsaKey(bits int) error {

	privateKeyDir := "/var/decept-agent/ssh/"
	privateKeyPath := "/var/decept-agent/ssh/private.pem"
	publicKeyPath := "/var/decept-agent/ssh/public.pem"

	var privateKeyFile *os.File
	var publicKeyFile *os.File

	if _, err := os.Stat(privateKeyDir); os.IsNotExist(err) {
		err = os.MkdirAll(privateKeyDir, os.ModePerm)
		if err != nil {
			logger.Error(err)
			return err
		}
	}

	if _, err := os.Stat(privateKeyPath); os.IsNotExist(err) {
		logger.Info("start create ssh key file===============")
		privateKey, err := rsa.GenerateKey(rand.Reader, bits)
		if err != nil {
			return err
		}
		derStream := x509.MarshalPKCS1PrivateKey(privateKey)
		block := &pem.Block{
			Type:  "RSA PRIVATE KEY",
			Bytes: derStream,
		}

		privateKeyFile, err = os.Create(privateKeyPath)
		if err != nil {
			logger.Error(err)
			return err
		}
		err = pem.Encode(privateKeyFile, block)
		if err != nil {
			logger.Error(err)

			return err
		}
		// 生成公钥文件
		publicKey := &privateKey.PublicKey
		derPkix, err := x509.MarshalPKIXPublicKey(publicKey)
		if err != nil {
			logger.Error(err)

			return err
		}
		block = &pem.Block{
			Type:  "RSA PUBLIC KEY",
			Bytes: derPkix,
		}
		publicKeyFile, err = os.Create(publicKeyPath)
		if err != nil {
			logger.Error(err)

			return err
		}
		err = pem.Encode(publicKeyFile, block)
		if err != nil {
			logger.Error(err)

			return err
		}
	}
	defer publicKeyFile.Close()
	defer privateKeyFile.Close()

	// 读取公钥文件 上传web端
	publicFileBytes, err := ioutil.ReadFile(publicKeyPath)
	if err != nil {
		logger.Error(err)
		return err
	}
	err, agentId := getAgentId()

	if err != nil {
		logger.Error(err)
	}
	logger.Info(string(publicFileBytes))
	keyString := GetBetweenStr(string(publicFileBytes), "-----BEGIN RSA PUBLIC KEY-----", "-----END RSA PUBLIC KEY-----")
	encodedData := base64.StdEncoding.EncodeToString([]byte(keyString))
	sshKeyBody := comm.SshKeyBody{
		SshKey:  encodedData,
		AgentId: agentId,
	}

	result := comm.HttpPost(AgentConf.SshKeyUploadUrl, sshKeyBody)
	logger.Info("upload ssh key result: %s", result)
	// TODO 如果上传不成功返回错误 一分钟之后再次上传
	if !strings.Contains(result, "成功") {
		return errors.New("upload fail")
	}
	return nil
}

func GetBetweenStr(str, start, end string) string {
	startLen := len(start)
	str = string([]byte(str)[startLen:])
	m := strings.Index(str, end)
	if m == -1 {
		m = len(str)
	}
	str = string([]byte(str)[:m])
	return str
}

/**
启动时对已存在的进程判断完全依赖 process-table 的数据
需要防止有进程突然中断 逃出process-table的记录
*/
func moduleLifecycleManagementTask() {
	logger.Info("start modules ")
	for {
		_, PidTable = comm.ReadProcessTable()
		proxyPid, proxyOk := PidTable[ProxyName]
		baitPid, baitOk := PidTable[BaitName]

		if comm.IsZombie(proxyPid.Id) {
			proxyPid.KillSelf()
		}
		if comm.IsZombie(baitPid.Id) {
			baitPid.KillSelf()
		}

		if ProxyEnable && (!proxyOk || !comm.ProcessIsExist(proxyPid.Id)) {
			proxyStart()
			time.Sleep(time.Millisecond * 500)
		}

		if BaitEnable && (!baitOk || !comm.ProcessIsExist(baitPid.Id)) {
			baitStart()
			time.Sleep(time.Millisecond * 500)
		}

		time.Sleep(time.Duration(30) * time.Second)
	}
}

func proxyStart() string {

	pid, err := comm.StartProject(ProxyPath, "", ProxyName)

	if err != nil {
		// TODO 启动不成功 处置
		logger.Error("proxy failed to start")
		return err.Error()
	}

	logger.Info("proxy started pid is [%d]", pid)
	return "success"
}

func baitStart() {

	pid, err := comm.StartProject(BaitPath, "", BaitName)

	if err != nil {
		// TODO 启动不成功 处置
		logger.Error("bait failed to start")
		return
	}

	logger.Info("bait started pid is [%d]", pid.Id)
}

var (
	agentCpuHis = make([]float64, 0)
	baitCpuHis  = make([]float64, 0)
	proxyCpuHis = make([]float64, 0)
)

func isMemOverloaded(name string, pid int, cpuLimit float64) bool {
	p, err := process.NewProcess(int32(pid))
	if err != nil {
		logger.Error(err)
		return false
	}

	memoryInfoStat, err := p.MemoryInfo()
	if err != nil {
		logger.Error("get [%s] memory info error: %s", name, err.Error())
		return false
	}
	memUsed := memoryInfoStat.RSS / 1024 / 1024
	if memUsed > 150 {
		logger.Error("mem usage: [%v] more than 150MB, exit", memUsed)
		return true
	}
	cpuP, err := p.CPUPercent()
	if err != nil {
		logger.Error("get cpu usage percent error: %s", err.Error())
		return false
	}

	avgCpu := cpuP / float64(runtime.NumCPU())

	cpu := fmt.Sprintf("%.2f", avgCpu)

	if avgCpu > cpuLimit {
		logger.Error("[%s] cpu usage: %s %s, Exit!", name, cpu, "%")
		if strings.Index(name, "decept-") == -1 {
			return true
		}
	}

	if avgCpu > 15 {
		logger.Error("[%s] cpu usage: %s %s, Exit Now!!!", name, cpu, "%")
		if strings.Index(name, "decept-") > -1 {
			return false
		}
	}

	logger.Info("[%s | %d] cpu usage: %v %s, limit threshold [%v], cpu-num: [%d], overall: [%v]", name, pid, cpu, "%", cpuLimit, runtime.NumCPU(), avgCpu)

	if strings.Index(name, "decept-") == -1 {
		return false
	}

	if strings.Index(name, "-proxy") > -1 {
		proxyCpuHis = append(proxyCpuHis, avgCpu)
		return isBigTooLong(name, proxyCpuHis, cpuLimit)
	} else if strings.Index(name, "-bait") > -1 {
		baitCpuHis = append(baitCpuHis, avgCpu)
		return isBigTooLong(name, baitCpuHis, cpuLimit)
	} else {
		agentCpuHis = append(agentCpuHis, avgCpu)
		return isBigTooLong(name, agentCpuHis, cpuLimit)
	}
}

func isBigTooLong(name string, cHis []float64, limit float64) bool {
	if len(cHis) > 5 {
		cHis = cHis[1:]
	}

	if len(cHis) == 5 {
		var isMoreThan = true
		for _, cpuAvgPercent := range cHis {
			if cpuAvgPercent < limit {
				isMoreThan = false
				break
			}
		}
		if isMoreThan {
			logger.Error("[%s] cpu usage: [%s] more than %v%s, exit", name, cHis, limit, "%")
			return true
		}
	}
	return false
}

func Exists(path string) bool {
	_, err := os.Stat(path)
	if err != nil {
		if os.IsExist(err) {
			return true
		}
		return false
	}
	return true
}

func heartBeat() {

	if AgentHeartBeat.AgentId == "" {
		err, agentId := getAgentId()

		if err != nil {
			logger.Error(err)
		}
		AgentHeartBeat.AgentId = agentId
	}

	if AgentHeartBeat.IPs == "" {
		ips, err := getIp()
		if SpecifyIP != "127.0.0.1" {
			ips = SpecifyIP + "," + ips
		}
		if err != nil {
			logger.Error(err)
		}
		AgentHeartBeat.IPs = ips
	}
	AgentHeartBeat.HostName, _ = os.Hostname()
	AgentHeartBeat.Type = StartMode
	client := comm.NewRedis(AgentConf.StrategyAddr, AgentConf.StrategyPass)
	pool := client.NewPool()
	for {
		time.Sleep(time.Second * 2)
		sentHeartBeat(AgentHeartBeat, client, pool)
		time.Sleep(time.Second * 58)
	}
}

func findSysUUID() string {
	cmd := exec.Command("/bin/sh", "-c", "sudo dmidecode -s system-uuid | tr 'A-Z' 'a-z'")
	output, err := cmd.Output()
	if err != nil {
		logger.Error(err.Error())
		return ""
	}
	agentId := string(output)
	agentId = strings.Replace(agentId, "\n", "", -1)
	if strings.Index(agentId, "-") < 0 {
		logger.Error("err agentid", agentId)
		return ""
	}
	return agentId
}

func getAgentId() (error, string) {

	var agentid string

	if Exists("conf/agent") == false {
		id := findSysUUID()
		if id == "" {
			uuid, err := uuid.GenerateUUID()
			if err != nil {
				logger.Error(err)
				return err, ""
			}
			id = uuid
		}

		host, err := os.Hostname()
		if err != nil {
			logger.Error(err)
			return err, ""
		}
		agentid = fmt.Sprintf("%s-%s", id, host)
		var f *os.File
		f, err = os.Create("conf/agent")
		if err != nil {
			logger.Error(err)
			return err, ""
		}
		defer f.Close()
		_, err = io.WriteString(f, agentid)
		if err != nil {
			logger.Error(err)
			return err, ""
		}
	} else {
		contents, err := ioutil.ReadFile("conf/agent")
		if err != nil {
			logger.Error(err)
			return err, ""
		}
		agentid = strings.Replace(string(contents), "\n", "", 1)
		logger.Info("agentid:", agentid)
	}

	return nil, agentid
}

func sentHeartBeat(beat comm.HeartBeat, client *comm.RedisServer, pool *redis.Pool) bool {

	conn := pool.Get()

	defer conn.Close()

	result, err := json.Marshal(beat)

	logger.Info(string(result))

	if err != nil {
		logger.Error("marshal heart beat err %v, %v", err, string(result))
		return false
	}

	encodedData := base64.StdEncoding.EncodeToString(result)

	logger.Info(encodedData)

	client.PublishMsg(pool, AgentConf.HeartbeatChannel, encodedData)

	return true

}

func getIp() (string, error) {
	var ipb []byte

	addrs, err := net.InterfaceAddrs()
	if err != nil {
		logger.Error(err)
		return "", err
	}

	limit := 0
	for _, address := range addrs {
		if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				ipb = append(ipb, []byte(ipnet.IP.String()+",")...)
			}
		}
		if limit > 3 {
			break
		} else {
			limit = limit + 1
		}
	}
	logger.Info("init ips %s", ipb)
	ip_len := len(ipb)
	if ip_len <= 0 {
		return "", errors.New("ip address is null")
	}

	ipb = ipb[:ip_len-1]
	return string(ipb), nil
}
