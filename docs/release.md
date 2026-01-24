# Make release

## Change release version

```shell
git commit --allow-empty -m "chore: release 2.0.0" -m "Release-As: 2.0.0"
```

## Update helm chart and documentation

```shell
git branch -D release-please--branches--main
git checkout release-please--branches--main
export `jq -r '"TAG=v"+.[]' hack/release-please-manifest.json`

sh hack/bump-chart-version.sh proxmox-csi-plugin false false true
make helm-unit docs

git add .
git commit -s --amend
```
