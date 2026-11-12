# Contributing to IWillBeGoogler backend

General rules (branching, Conventional Commits, review) live in the
[org guide](https://github.com/IWillBeGoogler/.github/blob/main/CONTRIBUTING.md). This file covers this repo.

## Setup

Requirements: Go (see `go.mod`), Docker.

```sh
cp .env.example .env
docker compose up -d --build   # postgres, redis, minio, emqx, migrate, api, worker
```

## Before you open a PR

```sh
make vet
make test          # unit tests, race detector on
make cover-gate    # coverage gate, as in CI
```

CI runs the same checks. Migrations go in `migrations/`, are written expand/contract, and need a working `Down`.
Indexes on existing tables are built `CONCURRENTLY` in a `-- +goose NO TRANSACTION` migration.

## Licence and your contributions

This project is **All Rights Reserved** (see [LICENSE](LICENSE)); it is not open source. By opening a pull request you confirm that:

1. you wrote the change yourself, or have the right to submit it, and it contains no code copied from elsewhere without a compatible licence;
2. you assign to IWillBeGoogler, or grant it a perpetual, worldwide, irrevocable, royalty-free, sublicensable licence to use, modify
   and distribute, all rights in your contribution, and you keep no claim against its use;
3. you sign off each commit (`git commit -s`, Developer Certificate of Origin 1.1: https://developercertificate.org).

Pull requests without a sign-off are not merged. Report vulnerabilities as described in [SECURITY.md](SECURITY.md) and follow the
[Code of Conduct](CODE_OF_CONDUCT.md).
