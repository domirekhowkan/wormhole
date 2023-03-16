#!/usr/bin/env bash

# This dev script imports an account, following the steps in `sui/NOTES.md`. 
# It also deploys the core/token bridge contracts.

# Remove directory for idempotency
rm -rf $HOME/.sui

# Import key so we have a deterministic address and make it the default account
sui keytool import "daughter exclude wheat pudding police weapon giggle taste space whip satoshi occur" ed25519
sui client << EOF
y
http://localhost:9000
dev
0
EOF
sed -i -e 's/active_address.*/active_address: "0x13b3cb89cf3226d3b860294fc75dc6c91f0c5ecf"/' ~/.sui/sui_config/client.yaml
echo Sui active address: $(sui client active-address)