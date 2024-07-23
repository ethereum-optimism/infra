# Proxyd Test Env

## About

* Use this folder to provision multiple local proxyds for test purposes
* It will deploy one proxyd condfigured to 3 upstream proxyds
  * Each of the 3 upstream proxyds can be configured to your RPC provider of choice
* Logs available using dozzel on localhost:8080
* Metrics available using prometheus on localhost:9090
* Dashboards available using grafana on localhost:3000

## Setup Instructions

1. Fillout env.template with RPC URLs
1. Copy env.template to .env, at the path infra/proxyd/tools/test-env/.env
1. Edit ./proxyd/proxyd/proxyd.toml.template and ./proxyd/upstream-proxyd-*/proxyd.toml.template as desired
1. Run ```./start-env.sh build``` to build and deploy current proxyd source code
1. Access proxyd using RPC calls to localhost:8001
