export AWS_ACCESS_KEY_ID = test
export AWS_SECRET_ACCESS_KEY = test
export AWS_REGION = us-east-1
export AWS_WITH_ENDPOINT = aws --endpoint-url=http://localhost:4566
export DOCKER_USER = USER_ID=$(shell id -u) GROUP_ID=$(shell id -g) 

.PHONY: start_kind start_docker_compose register_oidc check getOidcProvider wait_for_localstack 
.PHONY: start tear_down docker_build_push local_deploy

start_kind:
	$(info == STARTING KIND CLUSTER ==)
	sudo rm -rf ./k8s-pki
	mkdir ./k8s-pki
	kind create cluster --config ./kind-config.yml
	sudo chmod 644 ./k8s-pki/sa.* 

start_docker_compose:
	$(info == STARTING DOCKER-COMPOSE ==)
	${DOCKER_USER} docker-compose up -d 

register_oidc:
	$(info == REGISTERING OPENID CONNECT PROVIDER ==)
	${AWS_WITH_ENDPOINT} iam create-open-id-connect-provider --url https://hydra.local:4444 --client-id-list sts.amazonaws.com --thumbprint-list $(shell ./get-hydra-thumbprint.sh) 

check:
	$(info == CHECKING OPENID CONNECT PROVIDERS ==)
	${AWS_WITH_ENDPOINT} --no-cli-pager iam list-open-id-connect-providers

getOidcProvider:
	${AWS_WITH_ENDPOINT} --no-cli-pager iam list-open-id-connect-providers | jq -r '.OpenIDConnectProviderList[0].Arn' 

wait_for_localstack:
	$(info == WAITING FOR LOCALSTACK ==)
	./wait-for-localstack.sh
	$(info == localstack ready ==)

start: start_kind start_docker_compose wait_for_localstack register_oidc

tear_down:
	$(info == TEARING DOWN ==)
	kind delete clusters irsa-operator
	${DOCKER_USER} docker-compose down
	sudo rm -rf ./k8s-pki

docker_build_push:
	$(info == BUILDING OPERATOR DOCKER IMAGE ==)
	docker build -t localhost:5000/irsa-operator ..
	$(info == PUSHING OPERATOR IMAGE TO LOCAL REGISTRY ==)
	docker push localhost:5000/irsa-operator

local_deploy:
	OIDC=$$(${AWS_WITH_ENDPOINT} --no-cli-pager iam list-open-id-connect-providers | jq -r '.OpenIDConnectProviderList[0].Arn' $<); \
	kustomize build ../config/default | \
		CONTROLLER_IMG=localhost:5000/irsa-operator:latest \
		ROLE_ARN=not-applicable \
		CLUSTER_NAME=kind-cluster \
		OIDC_PROVIDER_ARN=$$OIDC envsubst | kubectl apply -f -


