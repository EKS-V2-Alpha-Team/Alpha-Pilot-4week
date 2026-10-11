# Contributing to EKS v2 – Order Fulfillment Platform

Thanks for your interest in contributing! This project is a 9-service order fulfillment platform on Amazon EKS, built with Terraform, Helm, GitHub Actions and ArgoCD. All kinds of contributions are welcome: bug reports, ideas, code and docs.

## Table of Contents

- [I Have a Question](#i-have-a-question)
- [I Want To Contribute](#i-want-to-contribute)
- [Reporting Bugs](#reporting-bugs)
- [Suggesting Enhancements](#suggesting-enhancements)
- [Your First Code Contribution](#your-first-code-contribution)
- [Improving The Documentation](#improving-the-documentation)
- [Styleguides](#styleguides)
- [Commit Messages](#commit-messages)
- [Join The Project Team](#join-the-project-team)

## I Have a Question

1. Read the README first.
2. Search existing [Issues](../../issues). Your question may already be answered.
3. If not, open a new [Issue](../../issues/new) with as much context as you can: what you were trying to do, what happened, and relevant tool versions (Terraform, kubectl, Helm).

## I Want To Contribute

### Reporting Bugs

#### Before Submitting a Bug Report

- Check you're on the latest `main`.
- Make sure it isn't a local setup issue (tool versions, AWS credentials, kube context).
- Search existing [Issues](../../issues) for the same bug.

#### How Do I Submit a Good Bug Report?

> **Security issues:** don't open a public issue. Report them privately through the repo's **Security** tab ("Report a vulnerability").

Open an [Issue](../../issues/new) and include:

- **What you expected** vs **what actually happened**
- **Steps to reproduce**
- **Relevant output**: errors, `terraform plan`, `kubectl` output, CI logs
- **Where it happens**: which service or module, which environment (dev / staging / prod)

Never paste secrets, credentials or AWS account IDs.

### Suggesting Enhancements

#### Before Submitting an Enhancement

- Search existing [Issues](../../issues) to see if it's already been suggested. If it has, add to that issue instead of opening a new one.
- Consider whether it fits the project's scope: a production-style EKS platform for the order fulfillment services.

#### How Do I Submit a Good Enhancement Suggestion?

Open an [Issue](../../issues/new) with:

- A **clear, descriptive title**
- **What you'd like to change** and **why it's useful**
- **What it affects**: services, infrastructure, Helm chart, or pipeline

For large changes, please open an issue to discuss before writing code.

### Your First Code Contribution

1. **Fork** the repo and clone your fork.
2. **Create a branch** off `main`: `feat/<what>`, `fix/<what>` or `docs/<what>`.
3. **Make your change and test it locally.** Everything runs without real AWS, so you get fast feedback before opening a PR. The Makefile wraps all the commands. Run `make help` to see every target, and see the README for first-time setup.

## Local testing

| You changed... | Run | What it checks |
|---|---|---|
| Service code | `make up` | The whole app runs end to end with Docker Compose + floci. See [README.md](README.md) |
| Helm chart, values or Kubernetes config | `make kind-render`, then `make kind-all` | Quick chart checks first (no cluster needed), then all nine services, Postgres, Redis, Traefik and floci running in a local kind cluster. See [local/kind/README.md](local/kind/README.md) |

4. **Open a pull request** into `main` explaining what changed, why, and how you tested it.
5. A maintainer will review it. PRs are merged once **CI passes** and they're **approved**.

### Improving The Documentation

Docs fixes are always welcome, from typos to clearer setup steps. They follow the same PR process. If your code change affects setup, deployment or configuration, update the docs in the same PR.

## Styleguides

### Commit Messages

Use a short prefix followed by what the change does:

```
feat: add readiness probe to shared chart
fix: correct RDS security group ingress rule
docs: clarify local setup steps
chore: bump terraform provider version
```

## Join The Project Team

This project was built by the Alpha team: Ahmed, Ramla, Jawwad, Naima and Paramjot. If you contribute regularly and want to help maintain it, open an issue or reach out to one of the maintainers.
