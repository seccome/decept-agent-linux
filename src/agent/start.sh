#!/bin/bash

function kill_if_process_exist(){
  PROC_NAME=$1
  echo "---------Start killing $PROC_NAME process and its child processes-----------"
  ProcNumber=$(ps -ef | grep $PROC_NAME | grep -v "grep" | awk '{print $2}')
  if [ $ProcNumber ]; then
    echo "进程ID: $ProcNumber"
    ps --ppid $ProcNumber | awk '{if($1~/[0-9]+/) print $1}' | xargs kill -9
    kill -9 $ProcNumber
    echo "--------------------End of killing process---------------------------"
  fi
}

function main() {
  echo "--------------------Start Deploying Ehoney Agent-----------------------------"
  echo "----                                                                    -----"
  echo "----     Please confirm that the conf/agent.json file is configured     -----"
  echo "----     Please check readme.md or the documentation for any questions  -----"
  echo "----                                                                    -----"
  echo "-----------------------------------------------------------------------------"
  
  yum install -y lsof
  
  kill_if_process_exist decept-agent

  Project_Dir=$(
    cd $(dirname $0)
    pwd
  )

  cd $Project_Dir

  nohup ./decept-agent >/dev/null &

  echo "--------------------Ehoney Agent Deployment complete--------------------------"
}

main
