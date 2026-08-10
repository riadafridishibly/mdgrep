---
title: Deployment notes
tags: [ops, kubernetes]
---

# Deployment

Some intro text about shipping the service to production.

## Prerequisites

- Install the CLI:
  - On macOS run `brew install foo`
  - On Linux download the tarball
- Configure credentials in `~/.foo/config`
- Verify with `foo doctor`

## Rolling out

Run the rollout in two phases.

```bash
foo deploy --canary 10
foo deploy --promote
```

> Rollbacks are cheap. Do not hesitate to revert a canary.

| Stage  | Traffic | Owner |
|--------|---------|-------|
| canary | 10%     | ops   |
| full   | 100%    | ops   |

## Troubleshooting

See [the runbook](https://example.com/runbook) for pager escalation.

### Pods stuck pending

Check node capacity first.
