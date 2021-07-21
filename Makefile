BUILDPATH=$(CURDIR)
GO=$(shell which go)
GOINSTALL=$(GO) install
GOBUILD=$(GO) build
GOCLEAN=$(GO) clean
GOGET=$(GO) get

AGENT_FILE=$(BUILDPATH)/src/agent/decept-agent.go
START_FILE=$(BUILDPATH)/src/agent/start.sh
TCP_PROXY_FILE=$(BUILDPATH)/src/agent/proxy/tcpProxy.go
PROXY_FILE=$(BUILDPATH)/src/agent/proxy/proxy.go
BAIT_FILE=$(BUILDPATH)/src/agent/bait/bait.go

AGENT_NAME=$(BUILDPATH)/bin/agent/decept-agent
TCP_PROXY_NAME=$(BUILDPATH)/bin/agent/proxy/tcpProxy
PROXY_NAME=$(BUILDPATH)/bin/agent/proxy/decept-proxy
BAIT_NAME=$(BUILDPATH)/bin/agent/bait/decept-bait


AGENT_DIR=$(BUILDPATH)/src/agent
PROXY_DIR=$(BUILDPATH)/src/agent/proxy
BAIT_DIR=$(BUILDPATH)/src/agent/bait


export GOPATH=$(CURDIR)

all: makedir build

makedir:
	@echo "start building tree..."
	@if [ ! -d $(BUILDPATH)/bin ] ; then mkdir -p $(BUILDPATH)/bin ; fi
	@if [ ! -d $(BUILDPATH)/bin/agent ] ; then mkdir -p $(BUILDPATH)/bin/agent ; fi
	@if [ ! -d $(BUILDPATH)/bin/agent/bait ] ; then mkdir -p $(BUILDPATH)/bin/agent/bait ; fi
	@if [ ! -d $(BUILDPATH)/bin/agent/bait/pkg ] ; then mkdir -p $(BUILDPATH)/bin/agent/bait/pkg ; fi
	@if [ ! -d $(BUILDPATH)/bin/agent/bait/conf ] ; then mkdir -p $(BUILDPATH)/bin/agent/bait/conf ; fi
	@if [ ! -d $(BUILDPATH)/bin/agent/proxy ] ; then mkdir -p $(BUILDPATH)/bin/agent/proxy ; fi
	@if [ ! -d $(BUILDPATH)/bin/agent/proxy/pkg ] ; then mkdir -p $(BUILDPATH)/bin/agent/proxy/pkg ; fi
	@if [ ! -d $(BUILDPATH)/bin/agent/proxy/conf ] ; then mkdir -p $(BUILDPATH)/bin/agent/proxy/conf ; fi
	@if [ ! -d $(BUILDPATH)/pkg ] ; then mkdir -p $(BUILDPATH)/pkg ; fi

build:
	@echo "start building..."
	@$(GOBUILD) -o $(AGENT_NAME) $(AGENT_FILE)
	@$(GOBUILD) -o $(BAIT_NAME) $(BAIT_FILE)
	@$(GOBUILD) -o $(PROXY_NAME) $(PROXY_FILE)
	@$(GOBUILD) -o $(TCP_PROXY_NAME) $(TCP_PROXY_FILE)

	@cp -r $(AGENT_DIR)/conf  $(BUILDPATH)/bin/agent/
	@cp $(AGENT_DIR)/start.sh  $(BUILDPATH)/bin/agent/start.sh
	@cp $(AGENT_DIR)/readme.md  $(BUILDPATH)/bin/agent/readme.md
	@cp -r $(BAIT_DIR)/conf  $(BUILDPATH)/bin/agent/bait/
	@cp -r $(PROXY_DIR)/conf  $(BUILDPATH)/bin/agent/proxy/
	@for dir in $(DIRS);    \
    do                      \
		[ -f "$$dir/Makefile" ] && $(MAKE) -C $$dir ;  \
		continue;\
    done
	@echo "Yay! all DONE!"

clean:
	@echo "cleanning"
	@rm -rf $(BUILDPATH)/bin/
	@rm -rf $(BUILDPATH)/pkg
	@$(GOCLEAN)


