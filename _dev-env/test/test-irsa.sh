echo "aws cli version :"
aws --version

echo
echo "aws env vars :"
env | grep AWS

echo
echo "get credentials"
aws --endpoint-url=http://aws-local:4566 sts assume-role-with-web-identity --role-arn $AWS_ROLE_ARN --role-session-name $(head /dev/urandom | tr -dc a-z | head -c10) --web-identity-token file://$AWS_WEB_IDENTITY_TOKEN_FILE --duration-seconds 1000 > /tmp/my-creds


echo 
echo "pass aws creds to env vars :"
export AWS_ACCESS_KEY_ID=$(cat /tmp/my-creds | jq -r '.Credentials.AccessKeyId')
export AWS_SECRET_ACCESS_KEY=$(cat /tmp/my-creds | jq -r '.Credentials.SecretAccessKey')
export AWS_SESSION_TOKEN=$(cat /tmp/my-creds | jq -r '.Credentials.SessionToken')

echo
echo "listable s3 buckets :"
aws --endpoint-url=http://aws-local:4566 s3 ls


echo
echo "should be forbidden according to role :"
aws --endpoint-url=http://aws-local:4566 iam us-east-1 list-roles 
