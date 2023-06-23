#!/bin/bash
set -eu

url=$(curl -s http://localhost:4040/api/tunnels | jq --raw-output ".tunnels[0].public_url")

if grep -q $url ngrok.url; then
    echo "URL already set"
else
    echo "Updating ngrok.url"
    echo -n $url > ngrok.url
fi