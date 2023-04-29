# Make relese

```shell
git checkout -b release-0.0.2
git tag v0.0.2

make helm-unit docs
make release-update

git add .
git commit
```
