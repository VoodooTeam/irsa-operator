create the infrastructure
```
terraform apply
```

build & push the docker image to the newly created ecr
```
make docker-build docker-push IMG=<ecr_url>/irsa:0.0.1
```
