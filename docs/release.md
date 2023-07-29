# Make relese

```shell
git checkout -b release-0.0.2
git tag v0.0.2

make helm-unit docs
make release-update

git add .
git commit
```

## Cosing verify Helm chart

We will verify the keyless signature using the Cosign protocol.

```shell
cosign verify ghcr.io/sergelogvinov/charts/proxmox-csi-plugin:0.1.4 --certificate-identity https://github.com/sergelogvinov/proxmox-csi-plugin/.github/workflows/release-charts.yaml@refs/heads/main --certificate-oidc-issuer https://token.actions.githubusercontent.com
```
