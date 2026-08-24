# Changelog

All notable changes to this project are documented here.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- `spec.resources` for the application container, and an automatic CPU request
  when autoscaling is enabled without one — CPU target utilization is a
  percentage of the request, so previously the HPA could never scale.
- `spec.readinessPath`, plus a default TCP readiness probe on `spec.port`, so
  `readyReplicas` and the `Available` condition reflect pods that can serve.
- `spec.security` (`runAsNonRoot`, `runAsUser`, `readOnlyRootFilesystem`) on top
  of always-on hardening: all capabilities dropped, no privilege escalation and
  the `RuntimeDefault` seccomp profile.
- `spec.podDisruptionBudget` to protect availability during node drains.
- `Progressing` and `Degraded` status conditions alongside `Available`, all
  carrying `observedGeneration`, and `status.readyReplicas`.
- Printer columns: `kubectl get webapp` now shows Image, Ready, Available, Age.
- Operand metrics `webapp_ready_replicas` and `webapp_info`, and a
  `PrometheusRule` with alerts for sustained reconcile errors, unavailable
  WebApps and a backing-up work queue.
- `make demo` for a scripted end-to-end walkthrough on a kind cluster.
- `docs/design.md` documenting the design decisions and their trade-offs.
- An invalid sample (`config/samples/platform_v1_webapp_invalid.yaml`) that
  demonstrates the validating webhook rejecting a mutable tag.

### Changed
- `spec.replicas` is now optional and defaults to 1; it was required even when
  autoscaling made it meaningless.
- Schema validation now matches the documented contract: `port` is bounded to
  1–65535, `replicas` must be non-negative, and `image` must be a plausible
  image reference.
- Status is written with a merge patch and only when it actually changed,
  instead of an unconditional update.
- The HorizontalPodAutoscaler and PodDisruptionBudget are deleted when their
  spec block is removed; owner references only clean up on parent deletion.
- The release workflow no longer publishes a `latest` tag: the operator rejects
  mutable tags for the workloads it manages and holds itself to the same rule.
- Samples and documentation pin an explicit image tag, so they satisfy the
  operator's own admission policy.

## [1.0.0] - 2026-08-21

### Added
- `WebApp` custom resource reconciling a Deployment, a Service and an optional
  HorizontalPodAutoscaler, with owner references for cascade deletion and
  self-healing.
- `Available` status condition and Kubernetes events on meaningful changes.
- Defaulting and validating admission webhooks: a managed-by label, and
  rejection of mutable image tags.
- Custom controller metrics and a Grafana dashboard.
- Helm chart, single-file installer, and CI covering tests, lint, e2e,
  chart install, image release and security scanning.
