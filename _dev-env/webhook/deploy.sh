export SCRIPT_DIR=$(dirname "$0")
export APP="pod-identity-webhook"
export NAMESPACE="default"
export CSR_NAME="${APP}.${NAMESPACE}.svc"
export CERT_FOLDER=${SCRIPT_DIR}/tls

# initial cleanup
rm -rf ${CERT_FOLDER}
mkdir ${CERT_FOLDER}

# we generate the private key and the CSR
openssl genrsa -out ${CERT_FOLDER}/webhook.key 2048
openssl req -new -config ${SCRIPT_DIR}/csr.conf -key ${CERT_FOLDER}/webhook.key -out ${CERT_FOLDER}/webhook.csr

# we create or replace the k8s CSR
kubectl delete csr ${CSR_NAME} || true 
export CSR_REQ=$(cat ${CERT_FOLDER}/webhook.csr | base64 | tr -d '\n')
cat ${SCRIPT_DIR}/csr.yml | envsubst | kubectl apply -f -

# we wait for the CSR to be created in k8s
while true; do
  kubectl get csr ${CSR_NAME} > /dev/null 2>&1
  if [ "$?" -eq 0 ]; then
      break
  fi
  echo "Waiting for CSR to be created" 
  sleep 1 
done

# we approve it
kubectl certificate approve ${CSR_NAME}

# we wait for the corresponding certificate to be issued
while true; do
  TLS_CERT=$(kubectl get csr ${CSR_NAME} -o jsonpath='{.status.certificate}')
  if [[ $TLS_CERT != "" ]]; then 
    break
  fi
  echo "Waiting for certificate to be issued" 
  sleep 1 
done

# we set the certificate and the private key as TLS secret on k8s
echo ${TLS_CERT} | openssl base64 -d -A -out ${CERT_FOLDER}/webhook.pem
kubectl delete secret webhook-tls-cert || true
kubectl create secret tls webhook-tls-cert --cert=${CERT_FOLDER}/webhook.pem --key=${CERT_FOLDER}/webhook.key

# grab the CA from k8s add it to the mutatingwebhook and create all the webhook related resources
export CA_BUNDLE=$(kubectl config view --raw --minify --flatten -o jsonpath='{.clusters[].cluster.certificate-authority-data}')
cat ${SCRIPT_DIR}/mutatingwebhook.tmpl | envsubst | kubectl apply -f - 
kubectl apply -f ${SCRIPT_DIR}/manifests/

# we restart the webhook (if any)
kubectl delete po -l app=pod-identity-webhook
