#!/bin/sh

CURRDIR=$(pwd)
IP=127.0.0.1
PRT=8443
CERT="$CURRDIR"/tests/certs/server-cert.pem
KEY="$CURRDIR"/tests/certs/server-key.pem

"$CURRDIR"/build/bin/darwin-amd64/wolfci "$IP":"$PRT" "$CERT" "$KEY"
