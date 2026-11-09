Dashboards tell you that something is wrong. Good alerts tell you that users are affected. Start from four signals for every service: latency, traffic, errors and saturation.

## Alert on symptoms

Page someone for a high error rate at the edge. Do not page anyone for high CPU that no user can notice.

```yaml
- alert: HighErrorRate
  expr: |
    sum(rate(http_requests_total{status=~"5.."}[5m]))
      / sum(rate(http_requests_total[5m])) > 0.02
  for: 5m
```
