package comm

import (
	"net"
	"testing"
	"util/zero"
)

func TestSendMsg(t *testing.T) {
	addr := ":5002"
	m, err := AttackMsg("wewewe", 1, `{"@timestamp":"2019-10-16T12:54:21.197Z","sequence":459,"category":"user-login","record_type":"cred_refr","result":"success","session":"22","summary":{"actor":{"primary":"0","secondary":"root"},"action":"refreshed-credentials","object":{"type":"user-session","primary":"ssh","secondary":"192.168.7.8"},"how":"/usr/sbin/sshd"},"user":{"ids":{"auid":"0","uid":"0"}},"process":{"pid":"24554","exe":"/usr/sbin/sshd"},"data":{"acct":"root","addr":"192.168.7.8","grantors":"pam_unix","hostname":"192.168.7.8","op":"PAM:setcred","terminal":"ssh"}}`, "127.0.0.1", "1234")
	if err != nil {
		panic(err)
	}

	msg := zero.NewMessage(ATTACK, []byte(m))
	data, err := zero.Encode(msg)
	if err != nil {
		panic(err)
	}

	tcpAddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		panic(err)
	}

	conn, err := net.DialTCP("tcp", nil, tcpAddr)
	if err != nil {
		panic(err)
	}

	defer conn.Close()
	conn.Write(data)

	//for i := 0; i < 100; i++ {
	//	n, err := conn.Write(data)
	//	if err != nil {
	//		panic(err)
	//	}
	//	fmt.Println("count: ", n)
	//	time.Sleep(time.Second)
	//}
}
