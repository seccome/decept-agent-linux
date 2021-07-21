# decept-agent

### 基本使用

    #启动
    nohup ./decept-agent &
    #查看日志：
    cat bin/agent/log/agent.log  agent日志


### agent配置

> 配置文件说明               
    {
      "honeyPublicIp": "",                    //蜜网的公网IP
      "strategyAddr": "127.0.0.1:6379",       //蜜网下发策略使用的Redis地址
      "strategyPass": "ehoney@0571",          //蜜网下发策略使用的Redis密码
      "version": "1.0",
      "heartbeatChannel": "agent-heart-beat-channel",  //心跳使用的Redis Channel 名称
      "sshKeyUploadUrl": "http://127.0.0.1:8082/deceptdefense/api/insertsshkey?t=1622516895107"   //协议代理上报SshKey的接口,只有协议代理才会用到
    }


### 其他说明

1. 修改conf/agent.json 中的strategyAddr 和strategyPass 值为正确的redis host地址和密码

2. 修改conf/agent.json sshKeyUploadUrl 值为正确的欺骗防御web端host地址

3. 如果探针与蜜网不在同一网段、需同时配置honeyPublicIp 的值

4. 注意同一台机器不能同时启动两个 decept-agent 除非修改conf/agent

5. 此agent不要部署在web端所在的机器

