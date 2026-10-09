#!/usr/bin/env bash
# Generates a local CA and a server certificate for the multica-notify hook
# endpoint. Multica's manifest validator only accepts https:// transport URLs,
# and for private origins the backend trusts whatever CA the operator names in
# MULTICA_PLUGIN_DEV_CA — that is the CA this script creates.
#
# Usage: gen-certs.sh <hostname-or-ip> [output-dir]
set -euo pipefail

TARGET=${1:?usage: gen-certs.sh <hostname-or-ip> [output-dir]}
OUT=${2:-.}
mkdir -p "$OUT"

SUBJ_CA="/CN=multica-notify dev CA"
SUBJ_SRV="/CN=${TARGET}"

# 1) Local CA (10 years).
if [[ ! -f "$OUT/ca.crt" ]]; then
  openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 \
    -keyout "$OUT/ca.key" -out "$OUT/ca.crt" -days 3650 -nodes -subj "$SUBJ_CA"
  echo "created CA: $OUT/ca.crt  (point MULTICA_PLUGIN_DEV_CA at this file)"
else
  echo "reusing existing CA: $OUT/ca.crt"
fi

# 2) Server key + CSR.
openssl req -newkey ec -pkeyopt ec_paramgen_curve:P-256 \
  -keyout "$OUT/tls.key" -out "$OUT/tls.csr" -days 825 -nodes -subj "$SUBJ_SRV"

# 3) SAN extension: DNS name, IP, or both when the target is an IP with a
#    resolvable name nearby — keep it simple: IP targets get an IP SAN, names
#    get a DNS SAN, and both forms are accepted as input.
if [[ "$TARGET" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  SAN="IP:$TARGET"
else
  SAN="DNS:$TARGET"
fi

openssl x509 -req -in "$OUT/tls.csr" -CA "$OUT/ca.crt" -CAkey "$OUT/ca.key" \
  -CAcreateserial -out "$OUT/tls.crt" -days 825 \
  -extfile <(printf "subjectAltName=%s\nextendedKeyUsage=serverAuth\n" "$SAN")

chmod 600 "$OUT/ca.key" "$OUT/tls.key"
rm -f "$OUT/tls.csr" "$OUT/ca.srl"
echo "wrote $OUT/tls.crt / $OUT/tls.key (SAN: $SAN)"
