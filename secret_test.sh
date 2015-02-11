#!/bin/bash

cat secret.json | _output/local/go/bin/kubectl create secrets --namespace=test -f -
cat pod.json | _output/local/go/bin/kubectl create --namespace=test -f -

