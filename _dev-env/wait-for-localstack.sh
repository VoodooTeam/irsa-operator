until AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test AWS_REGION=us-east-1 aws --no-cli-pager --endpoint-url=http://localhost:4566 sts get-caller-identity > /dev/null 2>&1; do
	sleep 1
done
