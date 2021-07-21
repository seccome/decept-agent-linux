package comm

import (
	"encoding/base64"
	"encoding/json"
	"strconv"
	"time"
)

type Message struct {
	Agentid string `json:"agentid"`
	Data    string `json:"data"`
}

type EventMsg struct {
	Eventtype  int    `json:"eventtype"`
	Msg        string `json:"msg"`
	Createtime string `json:"createtime"`
	Ip         string `json:"ip"`
	Port       int    `json:"port"`
	Rule       string `json:"rule"`
}

type AuditReport struct {
	Eventtype  int    `json:"eventtype"`
	Createtime string `json:"createtime"`
}

type AuditMsg struct {
	Timestamp   string   `json:"@timestamp"`
	Sequence    int      `json:"sequence"`
	Category    string   `json:"category"`
	Record_type string   `json:"record_type"`
	Result      string   `json:"result"`
	Session     string   `json:"session"`
	Tags        []string `json:"tags"`
	//Summary     interface{} `json:"summary"`
	//User        interface{} `json:"user"`
	Process AuditProcess `json:"process"`
	File    AuditFile    `json:"file"`
	Data    AuditData    `json:"data"`
	Paths   []AuditPath  `json:"paths"`
}

type AuditProcess struct {
	Pid   string `json:"pid"`
	Ppid  string `json:"ppid"`
	Title string `json:"title"`
	Name  string `json:"name"`
	Exe   string `json:"exe"`
	Cwd   string `json:"cwd"`
}

type AuditFile struct {
	Path   string `json:"path"`
	Device string `json:"device"`
	Inode  string `json:"inode"`
	Mode   string `json:"mode"`
	Uid    string `json:"uid"`
	Gid    string `json:"gid"`
}

type AuditData struct {
	A0      string `json:"a0"`
	A1      string `json:"a1"`
	A2      string `json:"a2"`
	A3      string `json:"a3"`
	Arch    string `json:"arch"`
	Argc    string `json:"argc"`
	Exit    string `json:"exit"`
	Syscall string `json:"syscall"`
	Tty     string `json:"tty"`
}

type AuditPath struct {
	CapFe      string `json:"cap_fe"`
	CapFi      string `json:"cap_fi"`
	CapFp      string `json:"cap_fp"`
	CapFrootid string `json:"cap_frootid"`
	CapFver    string `json:"cap_fver"`
	Dev        string `json:"dev"`
	Inode      string `json:"inode"`
	Item       string `json:"item"`
	Mode       string `json:"mode"`
	Name       string `json:"name"`
	Nametype   string `json:"nametype"`
	Ogid       string `json:"ogid"`
	Ouid       string `json:"ouid"`
	Rdev       string `json:"rdev"`
}

func BuildAttackMsg(agentId string, eventType int, msg string, ip string, port string) (string, error) {

	if ip == "" {
		ip, _ = GetIp()
	}
	if port == "" {
		port = "0"
	}

	intPort, err := strconv.Atoi(port)
	if err != nil {
		return "", err
	}
	eventMsg := EventMsg{
		Eventtype:  eventType,
		Msg:        msg,
		Createtime: time.Now().Format("2006-01-02 15:04:05"),
		Ip:         ip,
		Port:       intPort,
	}
	d, err := json.Marshal(eventMsg)
	if err != nil {
		return "", err
	}
	return GetMessage(agentId, d)
}

func AppLogMsg(agentId string, eventType int, msg string, ip string, port string, rule string) (string, error) {
	intPort, err := strconv.Atoi(port)
	if err != nil {
		return "", err
	}
	eventMsg := EventMsg{
		Eventtype:  eventType,
		Msg:        msg,
		Createtime: time.Now().Format("2006-01-02 15:04:05"),
		Ip:         ip,
		Port:       intPort,
		Rule:       rule,
	}
	d, err := json.Marshal(eventMsg)
	if err != nil {
		return "", err
	}
	return GetMessage(agentId, d)
}

func GetMessage(agentId string, data []byte) (string, error) {
	message := Message{
		Agentid: agentId,
		Data:    base64.StdEncoding.EncodeToString(data),
	}
	m, err := json.Marshal(message)
	if err != nil {
		return "", err
	}
	return string(m), nil
}

type AgentMonitorMsg struct {
	CPU        string `json:"cpu"`
	Mem        string `json:"mem"`
	Plugins    string `json:"plugins"`
	NetInRate  string `json:"netInRate"`
	NetOutRate string `json:"netOutRate"`
}
