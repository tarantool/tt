#!/usr/bin/env bash
set -e

### First argument is target directory where generate files.
DIR=$(realpath ${1:-$(pwd)})
[ -d $DIR ] || mkdir -p $DIR
cd $DIR

CA_SUBJ="/C=RU/ST=State/L=TestCity/O=Integration test/OU=Aeon/CN=localhost"

cat > ext.cnf << EOF
subjectAltName = @alt_names
[alt_names]
DNS = localhost
IP = 127.0.0.1
EOF

### Server .key, .crt and ca.crt required for Server-Side TLS mode.

# 1. Generate CA's private key and self-signed certificate
openssl req -new -x509 -days 1 -noenc -keyout ca.key -out ca.crt -subj "${CA_SUBJ}"

# 2. Generate web server's private key and certificate signing request (CSR)
openssl req -new -noenc -keyout server.key -out server.csr -subj "${CA_SUBJ}"

# 3. Use CA's private key to sign web server's CSR and get back the signed certificate
openssl x509 -req -in server.csr -days 1 -CA ca.crt -CAkey ca.key -CAcreateserial \
  -out server.crt -extfile ext.cnf

### Client .key & .crt required for Mutual TSL mode.

# 4. Generate client's private key and certificate signing request (CSR)
openssl req -new -noenc -keyout client.key -out client.csr -subj "$CA_SUBJ"

# 5. Use CA's private key to sign client's CSR and get back the signed certificate
openssl x509 -req -in client.csr -days 1 -CA ca.crt -CAkey ca.key -CAcreateserial \
  -out client.crt -extfile ext.cnf
