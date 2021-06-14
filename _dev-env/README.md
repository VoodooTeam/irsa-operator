# dev env 

## dependencies
- docker
- kind
- kubectl (>= 1.20)
- kustomize
- awscli v2
- gnumake
- jq
- openssl
- envsubst

## useful commands 

start the dev env 
```
make
```

update the operator
```
make update_operator
```

tear down the dev env
```
make clean
```

# what it does (when it starts)
- create the kind cluster
- start localstack
- start hydra (OIDC provider)
- register the oidc provider on AWS
- deploy the admission webhook (will use hydra)
- install the IRSA-operator CRDs
- deploy the irsa-operator
- deploy a test IamRoleServiceAccount CR and a deployment that uses it

## resources

https://blog.mikesir87.io/2020/09/eks-pod-identity-webhook-deep-dive/

https://www.eksworkshop.com/beginner/110_irsa/

https://docs.aws.amazon.com/IAM/latest/UserGuide/id_roles_providers_create_oidc_verify-thumbprint.html
