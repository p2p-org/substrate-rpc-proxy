##### deployment samples

Deployment samples provided only to familiarize proxy setup process and configuration.

* ansible
  ```
  ansible-playbook deployments/ansible/deploy.yaml -i localhost,
  ```
* kubernetes
  Please build your own image we will add some public registry later
  ```
  kustomize edit set image substrate-rpc-proxy=<yourimage:ver>
  kubectl apply -k deployments/kustomize/
  ```
