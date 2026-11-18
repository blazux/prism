#!/bin/sh
set -eu
umask 077
lineage=/etc/letsencrypt-lastserver/live/prism-demo
[ "${RENEWED_LINEAGE:-}" = "$lineage" ] || exit 0
target=/etc/haproxy/certs/prism-demo.pem
stage=$(mktemp /etc/haproxy/certs/.prism-demo.XXXXXX)
backup=$(mktemp /etc/haproxy/certs/.prism-demo-backup.XXXXXX)
had_previous=0
trap 'rm -f "$stage" "$backup"' EXIT HUP INT TERM
cert_public=$(openssl x509 -in "$lineage/fullchain.pem" -pubkey -noout | openssl pkey -pubin -outform DER | sha256sum)
key_public=$(openssl pkey -in "$lineage/privkey.pem" -pubout -outform DER | sha256sum)
[ "$cert_public" = "$key_public" ] || exit 1
openssl x509 -in "$lineage/fullchain.pem" -checkend 0 -noout >/dev/null
if [ -f "$target" ]; then cp "$target" "$backup"; had_previous=1; fi
cat "$lineage/fullchain.pem" "$lineage/privkey.pem" > "$stage"
chmod 0600 "$stage"
mv "$stage" "$target"
if grep -Fq "$target" /etc/haproxy/haproxy.cfg; then
    if haproxy -c -f /etc/haproxy/haproxy.cfg && systemctl reload haproxy; then exit 0; fi
    if [ "$had_previous" = 1 ]; then mv "$backup" "$target"; else rm -f "$target"; fi
    echo 'HAProxy reload failed; previous demo certificate restored.' >&2
    exit 1
fi
