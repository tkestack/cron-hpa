#!/bin/bash

# Copyright 2019 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#
# Generate a (self-signed) CA certificate and a certificate and private key to be used by the webhook server.
# The certificate will be issued for the Common Name (CN) of `webhook-server.{namespace}.svc`, which is the
# cluster-internal DNS name for the service.
#

set -e
set -x

if [ $# -ne 2 ]; then
    echo "Usage: $0 $key_dir $namespace"
    echo "    key_dir: used to put certs in"
    echo "    namespace: the namespace that webhook-server will be deployed in"
    exit 1
fi

key_dir="$1"
namespace="$2"

mkdir -p $key_dir
chmod 0700 $key_dir
cd $key_dir

SANCNF=san.cnf

cat << EOF > ${SANCNF}
[req]
distinguished_name = req_distinguished_name
req_extensions = v3_req
prompt = no

[req_distinguished_name]
C = CN
O = tkestack
CN = cron-hpa-controller.kube-system.svc

[v3_req]
keyUsage = critical, digitalSignature, keyEncipherment
extendedKeyUsage = clientAuth, serverAuth
subjectAltName = @alt_names
[alt_names]
DNS.1=cron-hpa-controller.kube-system.svc
EOF

# Generate the CA cert and private key
openssl req -nodes -new -x509 -days 100000 -keyout ca.key -out ca.crt -subj "/CN=Admission Webhook Server CA"
# Generate the private key for the webhook server
openssl genrsa -out tls.key 2048
# Generate a Certificate Signing Request (CSR) for the private key, and sign it with the private key of the CA.
openssl req -new -sha256 -days 100000 -key tls.key -subj "/cron-hpa-controller.kube-system.svc" -reqexts v3_req -config ${SANCNF} \
  | openssl x509 -req -days 100000 -CA ca.crt -CAkey ca.key -CAcreateserial -extensions v3_req  -extfile ${SANCNF}  -out tls.crt
