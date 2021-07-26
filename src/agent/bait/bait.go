package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/gomodule/redigo/redis"
	"gopkg.in/yaml.v2"
	"io/ioutil"
	"math/rand"
	"os"
	"strings"
	"time"
	"util/comm"
	"util/logger"
)

var (
	AgentHome       = comm.AgentHome()
	AgentConf       comm.AgentConf
	BaitLogConfPath = fmt.Sprintf("%s%s", AgentHome, "/bait/conf/log.json")
	PkgPath         = fmt.Sprintf("%s%s", AgentHome, "/bait/pkg/")
	SubChannel      = "deception-strategy-sub-channel"
	PubChannel      = "deception-strategy-pub-channel"
)

func main() {

	err := logger.SetLogger(BaitLogConfPath)

	if err != nil {
		// TODO 文件夹错误或读取异常
		logger.Error("logger setting err: ", err)
	}

	AgentConf = comm.LoadAgentConf(AgentHome)

	go comm.MonitForKillSelfTask()
	go comm.StartMemCpuMonitor("bait", 5)

	client := comm.NewRedis(AgentConf.StrategyAddr, AgentConf.StrategyPass)

	pool := client.NewPool()

	message := make(chan redis.Message, 1)

	go client.ListenChannel(pool, SubChannel, message)

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

			agentId := comm.QueryEngineId()

			if agentId != taskPayload.AgentID {
				continue
			}
			if taskPayload.TaskType == comm.BAIT {
				continue
			}

			var baitStrategy comm.BaitStrategy

			err = json.Unmarshal(data, &baitStrategy)

			if err != nil {
				logger.Error(err)
			}

			if baitStrategy.BaitType == comm.FILE_BAIT {
				var fileBaitPolicy comm.FileBaitDeployTaskPayload

				err := json.Unmarshal(data, &fileBaitPolicy)

				if err != nil {
					logger.Error("json unmarshal fail %v", err)
				}
				handleBaitAndCallback(fileBaitPolicy, client, pool)
			} else if baitStrategy.BaitType == comm.HIS_BAIT {

				var hisBaitPolicy comm.HistoryBaitDeployTaskPayload

				err = yaml.Unmarshal(data, &hisBaitPolicy)

				if err != nil {
					logger.Error("yaml unmarshal fail %v", err)
				}
				handleHistBaitAndCallBack(hisBaitPolicy, client, pool)
			} else {
				logger.Error("unknown bait type [%s]", baitStrategy.BaitType)
			}
		}
	}

}

func handleHistBaitAndCallBack(historyBaitStrategy comm.HistoryBaitDeployTaskPayload, redisClient *comm.RedisServer, pool *redis.Pool) {

	for _, honeyBaits := range historyBaitStrategy.HisBaitItem {
		if honeyBaits.Enabled == false {
			continue
		}
		BashHistoryPath := strings.Split(historyBaitStrategy.BashHistoryPath, ",")
		for _, value := range BashHistoryPath {
			if _, err := os.Stat(value); err != nil {
				if os.IsNotExist(err) {
					logger.Error(value, "file does not exist", err)
				}
			} else {
				//exist
				honeybit_creator(honeyBaits, value, historyBaitStrategy.RandomLine)
			}
		}
	}

	historyBaitStrategy.Status = comm.SUCCESS
	historyBaitStrategy.HisBaitItem = []comm.HisBaitItem{}

	result, err := json.Marshal(historyBaitStrategy)

	if err != nil {
		logger.Error("marshal file bait policy err %v, %v", err, historyBaitStrategy)
	}

	encodedData := base64.StdEncoding.EncodeToString(result)

	redisClient.PublishMsg(pool, PubChannel, encodedData)
	logger.Info("publish decode message [%s] result [%s]", encodedData, result)

}

func handleBaitAndCallback(fileBaitPolicy comm.FileBaitDeployTaskPayload, redisClient *comm.RedisServer, pool *redis.Pool) {

	// 下载文件位置使用MD5
	zipPath := fmt.Sprintf("%s%s.tar.gz", PkgPath, fileBaitPolicy.TaskID)

	urlByte, err := base64.StdEncoding.DecodeString(fileBaitPolicy.URL)

	success := comm.HttpDownload(string(urlByte), zipPath)

	if !success {
		logger.Error("download file err")
		fileBaitPolicy.Status = comm.FAILED
		callBackForResult(fileBaitPolicy, redisClient, pool)
		return
	}

	localMd5 := comm.MD5(zipPath)

	// 校验
	if localMd5 != fileBaitPolicy.FileMD5 {
		logger.Error("md5 check error proxy strategy md5:%s, local md5: %s", fileBaitPolicy.FileMD5, localMd5)
		fileBaitPolicy.Status = comm.FAILED
		callBackForResult(fileBaitPolicy, redisClient, pool)
		return
	}

	dir := fmt.Sprintf("%s%s/", PkgPath, fileBaitPolicy.TaskID)

	logger.Info("child dir: %s", dir)

	// 解压
	err = comm.Unzip(zipPath, dir)

	if err != nil {
		logger.Error("unzip file error: ", err)
		fileBaitPolicy.Status = -1
		callBackForResult(fileBaitPolicy, redisClient, pool)
		return
	} else {
		logger.Info("unzip file success ")
	}

	executeFilePath := fmt.Sprintf("%s%s", dir, "install.sh")

	logger.Info("executeFilePath: %s", executeFilePath)

	echoVar, err := comm.ExecFileForEcho(executeFilePath)

	if err != nil {
		logger.Error(err)
		fileBaitPolicy.Status = -1
		callBackForResult(fileBaitPolicy, redisClient, pool)
		return

	}
	// TODO 只要输出的不是 1  都认为执行失败

	logger.Info("echoVar: %s", echoVar)
	if echoVar == "1" {
		fileBaitPolicy.Status = 1
	} else {
		fileBaitPolicy.Status = -1
	}

	callBackForResult(fileBaitPolicy, redisClient, pool)
}

func callBackForResult(fileBaitPolicy comm.FileBaitDeployTaskPayload, redisClient *comm.RedisServer, pool *redis.Pool) {
	result, err := json.Marshal(fileBaitPolicy)

	if err != nil {
		logger.Error("marshal file bait policy err %v, %v", err, fileBaitPolicy)
	}

	encodedData := base64.StdEncoding.EncodeToString(result)

	redisClient.PublishMsg(pool, PubChannel, encodedData)
	logger.Info("publish decode message [%s] result [%s]", encodedData, result)
}

func rndline(l []string) int {
	s1 := rand.NewSource(time.Now().UnixNano())
	r1 := rand.New(s1)
	rl := r1.Intn(len(l))
	return rl
}

func contains(s []string, b string) bool {
	for _, a := range s {
		if a == b {
			return true
		}
	}
	return false
}

func linefinder(l []string, k string) int {
	linenum := 0
	for i := range l {
		if l[i] == k {
			linenum = i
		}
	}
	return linenum + 1
}

func honeybit_creator(hisBaitItem comm.HisBaitItem, hpath string, rnd string) {

	switch hisBaitItem.Htype {
	case "ssh":
		honeybit := fmt.Sprintf("ssh -p %s %s@%s",
			hisBaitItem.Port,
			hisBaitItem.User,
			hisBaitItem.Addr)

		insertBaits(hisBaitItem.Htype, hpath, honeybit, rnd)
	case "sshpass":
		honeybit := fmt.Sprintf("sshpass -p '%s' ssh -p %s %s@%s",
			hisBaitItem.Pass,
			hisBaitItem.Port,
			hisBaitItem.User,
			hisBaitItem.Addr)

		insertBaits(hisBaitItem.Htype, hpath, honeybit, rnd)
	case "wget":
		honeybit := fmt.Sprintf("wget %s",
			hisBaitItem.Url)

		insertBaits(hisBaitItem.Htype, hpath, honeybit, rnd)
	case "ftp":
		honeybit := fmt.Sprintf("ftp ftp://%s:%s@%s:%s",
			hisBaitItem.User,
			hisBaitItem.Pass,
			hisBaitItem.Addr,
			hisBaitItem.Port)

		insertBaits(hisBaitItem.Htype, hpath, honeybit, rnd)
	case "rsync":
		honeybit := fmt.Sprintf("rsync -avz -e 'ssh -p %s' %s@%s:%s %s",
			hisBaitItem.Port,
			hisBaitItem.User,
			hisBaitItem.Addr,
			hisBaitItem.RemotePath,
			hisBaitItem.LocalPath)

		insertBaits(hisBaitItem.Htype, hpath, honeybit, rnd)
	case "rsyncpass":
		honeybit := fmt.Sprintf("rsync -rsh=\"sshpass -p '%s' ssh -l %s -p %s\" %s:%s %s",
			hisBaitItem.Pass,
			hisBaitItem.User,
			hisBaitItem.Port,
			hisBaitItem.Addr,
			hisBaitItem.RemotePath,
			hisBaitItem.LocalPath)

		insertBaits(hisBaitItem.Htype, hpath, honeybit, rnd)
	case "scp":
		honeybit := fmt.Sprintf("scp -P %s %s@%s:%s %s",
			hisBaitItem.Port,
			hisBaitItem.User,
			hisBaitItem.Addr,
			hisBaitItem.RemotePath,
			hisBaitItem.LocalPath)

		insertBaits(hisBaitItem.Htype, hpath, honeybit, rnd)
	case "mysql":
		honeybit := fmt.Sprintf("mysql -h %s -P %s -u %s -p%s -e \"%s\"",
			hisBaitItem.Addr,
			hisBaitItem.Port,
			hisBaitItem.User,
			hisBaitItem.Pass,
			hisBaitItem.Command)

		insertBaits(hisBaitItem.Htype, hpath, honeybit, rnd)
	case "mysqldb":
		honeybit := fmt.Sprintf("mysql -h %s -u %s -p%s -D %s -e \"%s\"",
			hisBaitItem.Addr,
			hisBaitItem.User,
			hisBaitItem.Pass,
			hisBaitItem.DbName,
			hisBaitItem.Command)

		insertBaits(hisBaitItem.Htype, hpath, honeybit, rnd)
	case "alibaba":
		honeybit := fmt.Sprintf("export ALIBABA_ACCESS_KEY_ID=%s\nexport ALIBABA_ACCESS_KEY_SECRET=%s\nexport ALIBABA_REGION_ID=%s\n",
			hisBaitItem.Accesskeyid,
			hisBaitItem.Secretaccesskey,
			hisBaitItem.RegionId)
		insertBaits(hisBaitItem.Htype, hpath, honeybit, rnd)
	default:
		//custom
		cmdarr := strings.Split(hisBaitItem.Command, ",")
		for _, value := range cmdarr {
			insertBaits(hisBaitItem.Htype, hpath, value, rnd)
		}
	}
}

func insertBaits(ht string, fp string, hb string, rnd string) {
	if _, err := os.Stat(fp); os.IsNotExist(err) {
		_, err := os.Create(fp)
		if err != nil {
			logger.Error(err)
			return
		}
	}

	fi, err := ioutil.ReadFile(fp)
	if err != nil {
		logger.Error(err)
		return
	}

	var lines []string = strings.Split(string(fi), "\n")
	var hb_lines []string = strings.Split(string(hb), "\n")
	if iscontain := contains(lines, hb_lines[0]); iscontain == false {
		if rnd == "yes" {
			rl := (rndline(lines))
			lines = append(lines[:rl], append([]string{hb}, lines[rl:]...)...)
		} else if rnd == "no" {
			lines = append(lines, hb)
		}
		output := strings.Join(lines, "\n")
		err = ioutil.WriteFile(fp, []byte(output), 0644)
		if err != nil {
			logger.Info("[failed] Can't insert %s honeybit, error: \"%s\"\n", ht, err)
		} else {
			logger.Info("[done] %s honeybit is inserted(%s)\n", ht, output)
		}
	} else {
		logger.Warn("[failed] %s honeybit already exists\n", ht)
	}
}
