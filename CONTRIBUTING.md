# Contributing

## Pull Requests

All PRs require a single commit.

Having one commit in a Pull Request is very important for several reasons:
* A single commit per PR keeps the git history clean and readable.
  It helps reviewers and future developers understand the change as one atomic unit of work, instead of sifting through many intermediate or redundant commits.
* One commit is easier to cherry-pick into another branch or to track in changelogs.
* Squashing into one meaningful commit ensures the final PR only contains what matters.

## Developer Certificate of Origin

All commits require a [DCO](https://developercertificate.org/) sign-off.
This is done by committing with the `--signoff` flag.

## Development

The build process for this project is designed to run entirely in containers.
To get started, run `make help` and follow the instructions.

## Conformance

To verify conformance status, run `make conformance`.
This runs a series of tests on the working tree and is required to pass before a contribution is accepted.
