#!/bin/sh
# Make the secrets and the TLS certificates for compose.yaml in the
# folder secrets/. The script does not replace a file that exists. Thus
# you can put your own certificates into secrets/tls first:
#
#   secrets/tls/skriva.crt, skriva.key     the web server (with the chain)
#   secrets/tls/postgres.crt, postgres.key the database server
#   secrets/tls/ca.crt                     the CA of the database server
#                                          and of the OIDC provider
#
# The script makes the missing certificates with a private CA (valid for
# 10 years) in secrets/ca. Edit SKRIVA_BASE_URL in skriva.env first: the
# certificate of the web server gets its host name.
#
# Usage: sh make-secrets.sh 'the-oidc-client-secret'
set -eu

OIDC_SECRET=${1:?usage: make-secrets.sh OIDC-CLIENT-SECRET}
# The Skriva image of compose.yaml makes the certificates.
IMAGE=${SKRIVA_IMAGE:-$(sed -n 's/^    image: \(.*skriva.*\)$/\1/p' compose.yaml | head -n 1)}
HOST=$(sed -n 's|^SKRIVA_BASE_URL=https\{0,1\}://\([^:/]*\).*$|\1|p' skriva.env)

umask 077
mkdir -p secrets/tls secrets/ca
if [ ! -f secrets/db_password ]; then
	head -c 24 /dev/urandom | base64 | tr -d '/+=' >secrets/db_password
fi
if [ ! -f secrets/database_url ]; then
	printf 'postgres://skriva:%s@db:5432/skriva?sslmode=verify-full&sslrootcert=/tls/ca.crt\n' \
		"$(cat secrets/db_password)" >secrets/database_url
fi
if [ ! -f secrets/encryption_key ]; then
	head -c 32 /dev/urandom | base64 >secrets/encryption_key
fi
if [ ! -f secrets/oidc_client_secret ]; then
	printf '%s\n' "$OIDC_SECRET" >secrets/oidc_client_secret
fi

need_certs=false
for f in skriva.crt skriva.key postgres.crt postgres.key ca.crt; do
	[ -f "secrets/tls/$f" ] || need_certs=true
done
if [ "$need_certs" = true ]; then
	# In the container, user 0 is your user (rootless Podman). Thus you
	# own the files.
	podman run --rm --user 0 --read-only --network none \
		-v "$(pwd)/secrets:/out:z" "$IMAGE" \
		certs -dir /out/tls -ca-dir /out/ca -skriva-hosts "$HOST"
fi

# The containers do not run as your user. They must be able to read the
# files in secrets/ and secrets/tls. The folder secrets keeps other users
# of the computer out. The CA key in secrets/ca is only for you.
chmod 700 secrets secrets/ca
chmod 755 secrets/tls
find secrets -maxdepth 1 -type f -exec chmod 644 {} +
chmod 644 secrets/tls/*
chmod 600 secrets/ca/* 2>/dev/null || true
echo "The secrets are in $(pwd)/secrets."
echo "Make a backup of secrets/encryption_key: without it, nobody can read the documents."
if [ -f secrets/ca/ca.crt ]; then
	echo "The certificates are signed by the private CA secrets/ca/ca.crt."
	echo "Install it in the browsers and in the OIDC provider, or use your own certificates."
fi
