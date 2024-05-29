##### deployment samples

Deployment samples provided only to familiarize proxy setup process and configuration.

* minimal

  Will start proxy that forward requests to upstream1,upstream2.

  ```
  SUB_HOSTS=http+ws://upstream1:9944,http+ws://upstream2:9944 proxy
  ```


* ansible

  Setup remote server to be proxy

  ```
  ansible-playbook deployments/ansible/deploy.yaml -i localhost,
  ```

* kubernetes

  Please build your own image we will add some public registry later

  ```
  kustomize edit set image substrate-rpc-proxy=<yourimage:ver>
  kubectl apply -k deployments/kustomize/
  ```
