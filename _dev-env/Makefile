start_kind:
	sudo rm -rf ./k8s-pki
	mkdir ./k8s-pki
	kind create cluster --config ./kind-config.yml
	sudo chmod 644 ./k8s-pki/sa.* 

start_docker_compose:
	USER_ID=$(shell id -u) GROUP_ID=$(shell id -g) docker-compose up -d 

register_oidc:
	AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1 aws --endpoint-url=http://localhost:4566 iam create-open-id-connect-provider --url https://hydra.local:4444 --client-id-list sts.amazonaws.com --thumbprint-list $(shell ./get-hydra-thumbprint.sh) 

check:
	AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1 PAGER= aws --no-cli-pager --endpoint-url=http://localhost:4566 iam list-open-id-connect-providers

wait_for_localstack:
	./wait-for-localstack.sh
	echo "localstack ready"

start: start_kind start_docker_compose wait_for_localstack register_oidc

tear_down:
	kind delete clusters irsa-operator
	USER_ID=$(shell id -u) GROUP_ID=$(shell id -g) docker-compose down
	sudo rm -rf ./k8s-pki
