# dev env 

## caveats
- localstack (community edition) doesn't enforce IAM
- k8s version compatibility issue with

## clean up
```
sudo rm -rf ./k8s-pki
mkdir ./k8s-pki
```

## start the k8s cluster 

```
kind create cluster --config ./kind-config.yml
sudo chmod 644 ./k8s-pki/sa.* 
```

- it will create the kubernetes cluster, the `kind` docker network we'll join later, populate the `./k8s-pki/` folder with all the kubernetes pki keys.
- `kubectl get nodes` should return a `Ready` node.


## start the other services 

we'll start 3 other services : 
- aws localstack to fake aws 
- hydra to have an oidc provider 
- a local container registry (accessible from the outside at `localhost:5000`, from inside the `kind` network at `local-registry:5000`)

```
docker-compose up -d 
```

2 short-lived containers will :
- setup hydra's sqlite 
- load the serviceaccount `sa` keys in hydra

### check

a `docker ps` should only return only 3 containers : `hydra`, `aws-localstack` & `kind`

if you see one of the 2 other ones restarting, they have a problem, check their logs :
- `hydra-db-migrate` logs should print `Successfully applied migrations!`
- `hydra-add-keys` logs should print `JSON Web Key Set successfully imported!`

```
curl https://localhost:4444/.well-known/openid-configuration -k
curl https://localhost:4444/.well-known/jwks.json -k
```

should return no error

## register the oidc provider on aws 

register hydra as an oidc provider

```
export AWS_ACCESS_KEY_ID=test
export AWS_SECRET_ACCESS_KEY=test
export AWS_REGION=us-east-1
aws --endpoint-url=http://localhost:4566 iam create-open-id-connect-provider --url https://hydra.local:4444 --client-id-list sts.amazonaws.com --thumbprint-list $(./get-hydra-thumbprint.sh) 
```

NB : with set the client-id used by AWS to a value provided to the api-server (see ./kind-config.yml)

### check
```
aws --endpoint-url=http://localhost:4566 iam list-open-id-connect-providers
```
should return 

```
{
    "OpenIDConnectProviderList": [
        {
            "Arn": "arn:aws:iam::000000000000:oidc-provider/hydra.local:4444"
        }
    ]
}
```

you can also get details using 
```
aws --endpoint-url=http://localhost:4566 iam get-open-id-connect-provider --open-id-connect-provider-arn arn:aws:iam::000000000000:oidc-provider/hydra.local:4444
```

## aws setup
create : s3 bucket, upload this README, full-access to s3 bucket policy, role with the oidc provider, attach policy to role 

```
aws --endpoint-url=http://localhost:4566 s3api create-bucket --bucket irsa-test
aws --endpoint-url=http://localhost:4566 s3 cp ./README.md s3://irsa-test
aws --endpoint-url=http://localhost:4566 iam create-policy --policy-name my-test-policy --policy-document file://./test/policy.json
aws --endpoint-url=http://localhost:4566 iam create-role --role-name my-app-role --assume-role-policy-document file://./test/trust-role.json
aws --endpoint-url=http://localhost:4566 iam attach-role-policy --role-name my-app-role --policy-arn arn:aws:iam::000000000000:policy/my-test-policy
```

## setup the webhook

```
cd ./webhook
./deploy.sh
cd ..
```

## deploy irsa-tester
```
kubectl create -f ./test/irsa-tester.yml
```

### check
```
k exec irsa-tester -- env | grep AWS
```

should return
```
AWS_ROLE_ARN=arn:aws:iam::000000000000:role/my-app-role
AWS_WEB_IDENTITY_TOKEN_FILE=/var/run/secrets/eks.amazonaws.com/serviceaccount/token
```


## resources

https://blog.mikesir87.io/2020/09/eks-pod-identity-webhook-deep-dive/

https://www.eksworkshop.com/beginner/110_irsa/

https://docs.aws.amazon.com/IAM/latest/UserGuide/id_roles_providers_create_oidc_verify-thumbprint.html
