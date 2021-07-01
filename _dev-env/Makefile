export AWS_ACCESS_KEY_ID = test
export AWS_SECRET_ACCESS_KEY = test
export AWS_REGION = us-east-1
export AWS = aws --endpoint-url=http://localhost:4566 --no-cli-pager
export DOCKER_USER = USER_ID=$(shell id -u) GROUP_ID=$(shell id -g)

.PHONY: all start tear_down
.PHONY: start_kind start_docker_compose register_oidc check wait_for_localstack deploy_webhook
.PHONY: docker_build docker_push docker_build_push local_deploy update_operator restart_operator

all: clean start docker_build_push local_deploy create_irsa
start: start_kind gen_oidc_certs start_docker_compose wait_for_localstack register_oidc deploy_webhook
update_operator: docker_build_push local_deploy restart_operator

CERT_FOLDER=./oidc-provider/tls

restart_operator:
	kubectl delete po -n irsa-operator-system --all

create_irsa:
	kubectl apply -f ./irsa.yml

start_kind:
	$(info == STARTING KIND CLUSTER ==)
	sudo rm -rf ./k8s-pki
	mkdir ./k8s-pki
	kind create cluster --config ./kind-config.yml
	sudo chmod 644 ./k8s-pki/sa.*

gen_oidc_certs:
	rm -rf $(CERT_FOLDER)
	mkdir $(CERT_FOLDER)
	openssl genrsa -out $(CERT_FOLDER)/hydra.local.key 2048
	openssl req -new -config ./oidc-provider/csr.conf -key $(CERT_FOLDER)/hydra.local.key -out $(CERT_FOLDER)/hydra.local.csr
	openssl x509 -req -days 365 -in $(CERT_FOLDER)/hydra.local.csr -signkey $(CERT_FOLDER)/hydra.local.key -out $(CERT_FOLDER)/hydra.local.crt

start_docker_compose:
	$(info == STARTING DOCKER-COMPOSE ==)
	$(DOCKER_USER) docker-compose up -d

register_oidc:
	$(info == REGISTERING OPENID CONNECT PROVIDER ==)
	$(AWS) iam \
		create-open-id-connect-provider --url https://hydra.local:4444 --client-id-list sts.amazonaws.com --thumbprint-list \
		$(shell openssl s_client -connect localhost:4444 < /dev/null 2>/dev/null | openssl x509 -fingerprint -noout -in /dev/stdin | sed 's/.*=\|://g')

check:
	$(info == CHECKING OPENID CONNECT PROVIDERS ==)
	$(AWS) iam list-open-id-connect-providers

wait_for_localstack:
	$(info == WAITING FOR LOCALSTACK ==)
	until $(AWS) sts get-caller-identity > /dev/null 2>&1; do \
		sleep 1; \
	done

docker_build:
	$(info == BUILDING OPERATOR DOCKER IMAGE ==)
	docker build -t localhost:5000/irsa-operator ..

docker_push:
	$(info == PUSHING OPERATOR IMAGE TO LOCAL REGISTRY ==)
	docker push localhost:5000/irsa-operator

docker_build_push: docker_build docker_push

deploy_webhook:
	./webhook/deploy.sh

local_deploy:
	OIDC=$$($(AWS) iam list-open-id-connect-providers | jq -r '.OpenIDConnectProviderList[0].Arn'); \
	kustomize build ../config/default | \
		CONTROLLER_IMG=localhost:5000/irsa-operator:latest \
		ROLE_ARN=not-applicable \
		CLUSTER_NAME=kind-cluster \
		LOCALSTACK_ENDPOINT=http://aws-local:4566 \
		OIDC_PROVIDER_ARN=$$OIDC envsubst | kubectl apply -f -

clean:
	$(info == TEARING DOWN ==)
	kind delete clusters irsa-operator
	$(DOCKER_USER) docker-compose down
	sudo rm -rf ./k8s-pki

chaos: create_irsa
	rm -rf ./experiments_renders/
	mkdir ./experiments_renders/
	jsonnet ./experiments/_main.jsonnet -m ./experiments_renders/
	chaos run ./experiments_renders/*.json

