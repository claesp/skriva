#!/bin/sh
# Make the secret files for compose.yaml in the folder secrets/. The
# script does not replace a file that exists.
# Usage: sh make-secrets.sh 'the-oidc-client-secret'
set -eu

OIDC_SECRET=${1:?usage: make-secrets.sh OIDC-CLIENT-SECRET}
umask 077
mkdir -p secrets
if [ ! -f secrets/db_password ]; then
	head -c 24 /dev/urandom | base64 | tr -d '/+=' >secrets/db_password
fi
if [ ! -f secrets/database_url ]; then
	printf 'postgres://skriva:%s@db:5432/skriva?sslmode=disable\n' "$(cat secrets/db_password)" >secrets/database_url
fi
if [ ! -f secrets/encryption_key ]; then
	head -c 32 /dev/urandom | base64 >secrets/encryption_key
fi
if [ ! -f secrets/oidc_client_secret ]; then
	printf '%s\n' "$OIDC_SECRET" >secrets/oidc_client_secret
fi
# The containers do not run as your user. They must be able to read the
# files. The folder keeps other users out.
chmod 700 secrets
chmod 644 secrets/*
echo "The secrets are in $(pwd)/secrets. Make a backup of secrets/encryption_key:"
echo "without it, nobody can read the documents."
