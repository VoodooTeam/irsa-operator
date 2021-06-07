key & cert used for ory tls (mandatory to be added as an oidc provider on aws)


```
openssl genrsa -out hydra.local.key 4096
openssl req -new -key hydra.local -out hydra.local.csr

openssl req -new -x509 -sha256 -key key.pem -out cert.crt -days 365 -subj "/CN=hydra" 
```

(old school, should use SAN instead [https://geekflare.com/san-ssl-certificate/](https://geekflare.com/san-ssl-certificate/) )

## todo
CN should include port ? (`4444`)
