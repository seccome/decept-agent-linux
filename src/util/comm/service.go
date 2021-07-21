package comm

import "C"
import (
	"archive/zip"
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/go-redis/redis"
	"github.com/shirou/gopsutil/process"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"util/logger"
)

var ProcessTablePath = AgentHome() + "/conf/process-table.json"

func ProcessIsExist(pid int) bool {
	if err := syscall.Kill(pid, 0); err == nil {
		return true
	}
	logger.Info("process [%d] not exist", pid)
	return false
}

/*
	目前启动的项目有
	2. engine no-wait
	3. 边缘代理 no-wait
	4. 中继代理 no-wait
*/

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

func ReadProcessTable() (bool, map[string]Pid) {
	var processTable map[string]Pid
	if Exists(ProcessTablePath) == false {
		logger.Info("file %s not exists, create!", ProcessTablePath)
		var file *os.File
		file, err := os.Create(ProcessTablePath)
		if err != nil {
			logger.Error(err)
			return false, processTable
		}
		defer file.Close()
		_, err = io.WriteString(file, "{}")
		if err != nil {
			logger.Error("write string to file[%s] err: %v", ProcessTablePath, err)
			return false, processTable
		}
	}

	success, result := ReadFile(ProcessTablePath)

	if !success {
		return success, processTable
	}
	err := json.Unmarshal([]byte(result), &processTable)

	if err != nil {
		logger.Error(err)
		return false, processTable
	}

	return true, processTable
}

func UpdateProcessTable(processTable map[string]Pid) {
	FlushData2File(processTable, ProcessTablePath)
}

type ICMP struct {
	Type        uint8
	Code        uint8
	Checksum    uint16
	Identifier  uint16
	SequenceNum uint16
}

func IsIpConnect(ip string) bool {
	if connect(ip) {
		logger.Info("connect %s success", ip)
		return true
	} else {
		logger.Info("connect %s fail", ip)
	}
	return false
}

func connect(ip string) bool {
	cmd := exec.Command("ping", ip, "-c", "1", "-W", "5")
	err := cmd.Run()
	if err != nil {
		logger.Info("NetWorkStatus Error:", ip, err.Error())
		return false
	}
	return true
}

func CheckSum(data []byte) uint16 {
	var (
		sum    uint32
		length int = len(data)
		index  int
	)
	for length > 1 {
		sum += uint32(data[index])<<8 + uint32(data[index+1])
		index += 2
		length -= 2
	}
	if length > 0 {
		sum += uint32(data[index])
	}
	sum += sum >> 16

	return uint16(^sum)
}

func RedisGet(key string, AgentConf AgentConf) (success bool, data string) {
	client := redis.NewClient(&redis.Options{
		Addr:     AgentConf.StrategyAddr,
		Password: AgentConf.StrategyPass,
		DB:       0,
	})

	pong, err := client.Ping().Result()

	if err != nil {
		logger.Error(pong, err)
	}

	defer client.Close()

	data, err = client.Get(key).Result()
	if err == redis.Nil {
		logger.Info("redis [%s] does not exist", key)
		return false, data
	} else if err != nil {
		logger.Error(err)
		return false, data
	}
	return true, data
}

func RedisSet(key, value string, AgentConf AgentConf) bool {
	client := redis.NewClient(&redis.Options{
		Addr:     AgentConf.StrategyAddr,
		Password: AgentConf.StrategyPass,
		DB:       0,
	})

	pong, err := client.Ping().Result()

	if err != nil {
		logger.Error(pong, err)
	}

	defer client.Close()

	err = client.Set(key, value, 0).Err()
	if err != nil {
		logger.Error(err)
		return false
	}
	return true
}

func MonitForKillSelfTask() {
	processId := os.Getpid()
	p, err := process.NewProcess(int32(processId))
	if err != nil {
		logger.Fatal(err)
	}

	logger.Info("start monit process [%d] kill-self-task", processId)

	for {
		kill := true
		time.Sleep(time.Second * 10)
		_, PidTable := ReadProcessTable()

		for _, pid := range PidTable {
			if pid.Id == processId {
				kill = false
			}
		}

		if kill {
			logger.Fatal("this processes [%v] [%d] that are not included in process-table, killed by self", p, processId)
		}
	}
}

func IsZombie(processId int) bool {

	if processId == 0 || !ProcessIsExist(processId) {
		return false
	}
	p, err := process.NewProcess(int32(processId))
	if err != nil {
		logger.Error(err)
	} else {
		status, err := p.Status()
		if err != nil {
			logger.Error(err)
		}
		logger.Debug("process [%d] is [%s]", processId, status)
		if "Z" == status {
			return true
		}
	}
	return false
}

func StartProject(projectPath, mode, nickName string) (Pid, error) {

	var startedPid Pid

	var argv []string

	var process *os.Process

	var err error

	var processResult string

	logger.Info("starting project [%s] mode [%s] nick name [%s]", projectPath, mode, nickName)

	startedPid.Mode = mode

	success, processTable := ReadProcessTable()

	if !success {
		logger.Error("read process table fail")
		return startedPid, nil
	}

	defer UpdateProcessTable(processTable)

	if pid, ok := processTable[nickName]; ok {
		err := pid.KillSelf()
		if err != nil {
			logger.Error(err)
		}
	}
	procAttr := &os.ProcAttr{
		Env: os.Environ(),
		Files: []*os.File{
			os.Stdin,
			os.Stdout,
			os.Stderr,
		},
	}

	if nickName != "" {
		if index := strings.Index(nickName, "-"); index > -1 {
			rawStrSlice := []byte(nickName)
			argv = []string{string(rawStrSlice[:index])}
		} else {
			argv = []string{nickName}
		}
	} else {
		argv = nil
	}

	if mode == "bash" {

		fmtCmd := exec.Command("sh", "-c", projectPath)

		if err = fmtCmd.Start(); err != nil {
			return startedPid, err
		}

		tail := 3

		for {
			if tail > 1 {
				time.Sleep(100)
				process = fmtCmd.Process
				if process != nil {
					break
				}
			} else {
				break
			}
			tail = tail - 1
		}

		process = fmtCmd.Process

		if process == nil {
			startedPid.Result = "start error"
			logger.Error(err)
			return startedPid, err
		}
		processResult = "start success"

	} else {
		process, err = os.StartProcess(projectPath, argv, procAttr)
		if err != nil {
			logger.Error(err)
			processResult = fmt.Sprintf("start error [%v]", err)
		} else {
			processResult = "start success"
		}
	}

	if process == nil {
		return startedPid, err
	}

	startedPid = Pid{
		Id:     process.Pid,
		Name:   nickName,
		Result: processResult,
		Mode:   mode,
	}

	processTable[nickName] = startedPid

	return startedPid, err
}

func MD5(filename string) string {
	var filePath string
	if strings.Index(filename, "/") > -1 {
		filePath = filename
	} else {
		filePath = fmt.Sprintf("./%s", filename)
	}

	pFile, err := os.Open(filePath)
	if err != nil {
		logger.Error("open fill err, filename=%v, err=%v", filename, err)
		return ""
	}
	defer pFile.Close()
	md5h := md5.New()
	io.Copy(md5h, pFile)

	return hex.EncodeToString(md5h.Sum(nil))
}

func Unzip(zipFile string, destDir string) error {

	file, err := os.Open(destDir)
	defer func() {
		if file != nil {
			err = file.Close()
			if err != nil {
				logger.Error(err)
			}
		}
	}()

	if (err != nil && os.IsNotExist(err)) || file == nil {
		logger.Info("%s dir not exist, create ", destDir)
		err = os.MkdirAll(destDir, os.ModePerm)
		if err != nil {
			logger.Error(err)
			return err
		}
	}

	cmd := exec.Command("/bin/sh", "-c", "chmod -R 777 "+destDir)
	_, err = cmd.Output()
	if err != nil {
		logger.Error(err.Error())
	}

	if strings.Contains(zipFile, ".tar.gz") {
		// openssl des3 -d -k 123 -salt -in ~/work-space/engine1122.tar | tar xzf -
		cmdStr := fmt.Sprintf("tar -zxvf %s -C %s", zipFile, destDir)
		logger.Info("unzip cmd: %s", cmdStr)
		cmd = exec.Command("/bin/sh", "-c", cmdStr)
		cmd.Dir = destDir
		_, err := cmd.Output()
		if err != nil {
			logger.Error(err.Error())
		}
		return nil
	} else if strings.Contains(zipFile, ".zip") {
		cmdStr := fmt.Sprintf("unzip -o %s -d %s", zipFile, destDir)
		logger.Info("unzip cmd: %s", cmdStr)
		cmd := exec.Command("/bin/sh", "-c", cmdStr)
		cmd.Dir = destDir
		_, err := cmd.Output()
		if err != nil {
			logger.Error(err.Error())
		}
		return nil
	}

	zipReader, err := zip.OpenReader(zipFile)
	if err != nil {
		return err
	}
	defer zipReader.Close()

	for _, f := range zipReader.File {
		fpath := filepath.Join(destDir, f.Name)
		if f.FileInfo().IsDir() {
			os.MkdirAll(fpath, os.ModePerm)
		} else {
			if err = os.MkdirAll(filepath.Dir(fpath), os.ModePerm); err != nil {
				return err
			}

			inFile, err := f.Open()
			if err != nil {
				return err
			}
			defer inFile.Close()

			outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
			if err != nil {
				return err
			}
			defer outFile.Close()

			_, err = io.Copy(outFile, inFile)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// 发送POST请求
// url：         请求地址
// data：        POST请求提交的数据
// contentType： 请求体格式，如：application/json
// content：     请求放回的内容
func HttpPost(url string, data interface{}) string {
	client := &http.Client{Timeout: 5 * time.Second}
	jsonStr, _ := json.Marshal(data)
	resp, err := client.Post(url, "application/json", bytes.NewBuffer(jsonStr))
	if err != nil {
		logger.Error(err)
		return err.Error()
	}
	defer resp.Body.Close()

	result, _ := ioutil.ReadAll(resp.Body)
	return string(result)
}

func HttpDownload(url, fileName string) bool {
	logger.Info("try download file url: %s", url)
	res, err := http.Get(url)
	if err != nil {
		logger.Error(err)
		return false
	}
	file, err := os.Create(fileName)
	if err != nil {
		logger.Error(err)
		return false
	}
	_, wErr := io.Copy(file, res.Body)
	if wErr != nil {
		logger.Error(err)
		return false
	}
	return true
}

func ExecFileForEcho(projectPath string) (string, error) {
	cmd := exec.Command("/bin/bash", "-c", projectPath)
	logger.Info("Exec [%v]", cmd)
	output, err := cmd.Output()
	if err != nil {
		logger.Error(err, output)
		return "0", err
	}
	logger.Info("Execute Shell Echo: %s", string(output))
	return string(output), err
}

func CheckPort(port string) bool {
	checkStatement := fmt.Sprintf("lsof -i:%s ", port)
	output, _ := exec.Command("sh", "-c", checkStatement).CombinedOutput()
	if len(output) > 0 {
		return true
	}
	return false
}

func FlushAgentConfig(agentConf AgentConf, agentConfPath string) {
	var f *os.File
	var err error
	f, err = os.OpenFile(agentConfPath, os.O_WRONLY|os.O_TRUNC, os.ModeExclusive)

	if err != nil {
		logger.Error(err)
		return
	}

	defer f.Close()
	b, e := json.Marshal(agentConf)
	if e != nil {
		return
	}
	str := string(b)
	logger.Info("flush agent conf file: %s", str)
	_, err = io.WriteString(f, str)
	if err != nil {
		logger.Error(err)
	}
}

func FlushData2File(proxyTable interface{}, filePath string) {
	var f *os.File
	var err error
	f, err = os.OpenFile(filePath, os.O_WRONLY|os.O_TRUNC, os.ModeExclusive)

	if err != nil {
		logger.Error(err)
		return
	}

	defer f.Close()
	b, e := json.Marshal(proxyTable)
	if e != nil {
		return
	}
	str := string(b)
	logger.Info("flush str: %s to file: %s", str, filePath)
	_, err = io.WriteString(f, str)
	if err != nil {
		logger.Error(err)
	}
}

var (
	cpuHis = make([]float64, 0)
)

func StartMemCpuMonitor(name string, cpuLimit float64) {
	pid := os.Getpid()
	p, err := process.NewProcess(int32(pid))
	if err != nil {
		logger.Fatal(err)
	}
	logger.Info("start mem and cpu monitor, pid: %v process: %s", pid, name)

	for {

		time.Sleep(time.Second * 5)

		memoryInfoStat, err := p.MemoryInfo()
		if err != nil {
			logger.Fatal("get %s memory info error: %s", name, err.Error())
		}
		memUsed := memoryInfoStat.RSS / 1024 / 1024
		if memUsed > 150 {
			logger.Fatal("process %s mem usage: [%v] more than 150MB, exit", name, memUsed)
		}

		cpuOverallPercent, err := p.Percent(0)
		if err != nil {
			logger.Fatal("get process %s cpu usage percent error: %s", name, err.Error())
		}

		avgCpu := cpuOverallPercent / float64(runtime.NumCPU())

		cpu := fmt.Sprintf("%.2f", avgCpu)

		if avgCpu > 5 {
			logger.Warn("process %s cpu usage: %s %s", name, cpu, "%")
		}

		logger.Debug("pid[%d] process [%s] overall[%v] cpu usage: %s %s", pid, name, cpuOverallPercent, cpu, "%")

		cpuHis = append(cpuHis, avgCpu)

		if len(cpuHis) > 6 {
			cpuHis = cpuHis[1:]
		}
		if len(cpuHis) == 6 {
			var isMoreThan = true
			for _, cpuAvgPercent := range cpuHis {
				if cpuAvgPercent < cpuLimit {
					isMoreThan = false
					break
				}
			}
			if isMoreThan {
				logger.Fatal("process [%s] cpu usage: [%s] more than %v%s, exit", name, cpu, cpuLimit, "%")
			}
		}
	}

}

func GetIp() (string, error) {
	var ipb []byte

	addrs, err := net.InterfaceAddrs()
	if err != nil {
		logger.Error(err)
		return "", err
	}

	for _, address := range addrs {
		if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				ipb = append(ipb, []byte(ipnet.IP.String()+",")...)
			}
		}
	}

	ip_len := len(ipb)
	if ip_len <= 0 {
		return "", errors.New("ip address is null")
	}

	ipb = ipb[:ip_len-1]
	return string(ipb), nil
}
