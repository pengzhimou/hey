#./hey -n 100 -c 10 -t 30 -m GET -urlfile ./urlfile.txt 每一个url都为独立的测试对象，总测试并发数=conc*url数量

#./hey -n 100 -c 10 -t 30 -m GET -url http://x.x.x.x


#./hey -n 100 -c 50 -t 30 -m POST  -H 'X-TKE-ClusterName: cls-ogsfrmms' -H 'Content-Type: application/json' -cert ./xxx.pem -key .xxx-key.pem -url https://xxxxxxx/configmaps -D xxx.json -randmark "HEY"

#./hey -n 100 -c 50 -t 30 -m DELETE  -H 'X-TKE-ClusterName: cls-ogsfrmms' -H 'Content-Type: application/json' -cert ./xxx.pem -key ./xxx-key.pem -url https://xxxxxxxxxx/cm-HEY-v0.0.1  -randmark "HEY"


# xxx.json:
# {
#   "apiVersion": "v1",
#   "data": {
#     "b": "c111",
#     "d": "e111"
#   },
#   "kind": "ConfigMap",
#   "metadata": {
#     "annotations": {
#       "xxxxx/configmap-name": "cm-HEY",
#       "xxxxxxx/configmap-version": "v0.0.1"
#     },
#     "name": "cm-HEY-v0.0.1",
#     "namespace": "ns-tttttttttttt"
#   }
# }