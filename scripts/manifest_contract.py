#!/usr/bin/env python3
"""Contract checks that neither kustomize nor kubeconform can see.

Three historical failure modes motivate these:

1. A Deployment declares the same env var name twice. The API server keeps the
   last value, so the overlay and the effective configuration disagree
   (RHOAIENG-57579: duplicate RELATED_IMAGE_CRON_JOB).
2. The image ships manifests to one directory and the operator is told to read
   another (RHOAIENG-99568: manifests under /opt/manifests/modelregistry while
   the operator read /opt/manifests/aihub).
3. The generated Helm chart drifts from the kustomize output it is built from
   (RHOAIENG-96436: odh-feast-operator manifests out of sync).
4. A RoleBinding or ClusterRoleBinding names a ServiceAccount that the chart
   does not create, because a kustomize namePrefix rewrites the ServiceAccount
   but not the binding subjects (RHOAIENG-100514: feast-operator crash-looped
   on reading apiservers.config.openshift.io; RHOAIENG-100515: DSC NotReady).
"""

import argparse
import re
import sys

try:
    import yaml
except ImportError:  # pragma: no cover - handled by the caller
    sys.exit("PyYAML is required (pip install pyyaml)")


def containers(document):
    kind = document.get("kind")
    if kind not in {"Deployment", "StatefulSet", "DaemonSet", "Job", "CronJob"}:
        return
    spec = document.get("spec") or {}
    if kind == "CronJob":
        spec = (spec.get("jobTemplate") or {}).get("spec") or {}
    pod_spec = ((spec.get("template") or {}).get("spec") or {})
    for name in ("initContainers", "containers"):
        for container in pod_spec.get(name) or []:
            yield document.get("metadata", {}).get("name", "?"), container


def load(path):
    with open(path, encoding="utf-8") as handle:
        for document in yaml.safe_load_all(handle):
            if isinstance(document, dict):
                yield document


def duplicate_env(paths):
    failures = []
    for path in paths:
        for name, container in (c for d in load(path) for c in containers(d)):
            seen = set()
            for entry in container.get("env") or []:
                key = entry.get("name")
                if key is None:
                    continue
                if key in seen:
                    failures.append(
                        f"{path}: Deployment/{name} container "
                        f"{container.get('name')} declares env var {key} more than once"
                    )
                seen.add(key)
    return failures


def rbac_subjects(paths):
    failures = []
    for path in paths:
        documents = list(load(path))
        service_accounts = {
            (document.get("metadata") or {}).get("name")
            for document in documents
            if document.get("kind") == "ServiceAccount"
        }
        service_accounts.discard(None)
        if not service_accounts:
            # Nothing to compare against; the chart may expect the platform to
            # create the identity.
            continue

        for document in documents:
            kind = document.get("kind")
            name = (document.get("metadata") or {}).get("name", "?")
            if kind in {"RoleBinding", "ClusterRoleBinding"}:
                for subject in document.get("subjects") or []:
                    if subject.get("kind") != "ServiceAccount":
                        continue
                    account = subject.get("name")
                    if account not in service_accounts:
                        failures.append(
                            f"{path}: {kind}/{name} binds to ServiceAccount "
                            f"{subject.get('namespace')}/{account}, which the chart does not create"
                        )
            if kind in {"Deployment", "StatefulSet", "DaemonSet"}:
                pod_spec = (
                    ((document.get("spec") or {}).get("template") or {}).get("spec") or {}
                )
                account = pod_spec.get("serviceAccountName")
                if account and account not in service_accounts:
                    failures.append(
                        f"{path}: {kind}/{name} runs as ServiceAccount {account}, "
                        "which the chart does not create"
                    )
    return failures


def containerfile_manifest_dir(path):
    """Destination the Containerfile copies config/manifests/ to."""
    with open(path, encoding="utf-8") as handle:
        text = handle.read()
    matches = re.findall(
        r"^COPY\s+(?:--from=\S+\s+)?\S*config/manifests/?\s+(/\S+)", text, re.MULTILINE
    )
    return matches[-1].rstrip("/") if matches else None


def chart_manifest_paths(path):
    """(copy source dir, copy destination dir, MANIFESTS_PATH env value)."""
    source = destination = env_value = None
    for document in load(path):
        for _, container in containers(document):
            argv = list(container.get("command") or []) + list(container.get("args") or [])
            for index, token in enumerate(argv):
                if token != "cp":
                    continue
                operands = [t for t in argv[index + 1 :] if not t.startswith("-")]
                pair = operands[:2]
                if len(pair) == 2 and pair[0].endswith("/.") and pair[1].startswith("/"):
                    source, destination = pair[0][:-2].rstrip("/"), pair[1].rstrip("/")
            for entry in container.get("env") or []:
                if entry.get("name") == "ODH_MODULE_OPERATOR_MANIFESTS_PATH":
                    env_value = entry.get("value")
    return source, destination, env_value


def manifest_path_contract(containerfile, chart_deployment):
    image_dir = containerfile_manifest_dir(containerfile)
    source, destination, env_value = chart_manifest_paths(chart_deployment)

    problems = []
    if not image_dir:
        problems.append(f"{containerfile}: no COPY of config/manifests/ into an absolute path")
    if not source or not destination:
        problems.append(f"{chart_deployment}: no 'cp -r <dir>/. <dir>/' init container found")
    if not env_value:
        problems.append(f"{chart_deployment}: ODH_MODULE_OPERATOR_MANIFESTS_PATH not set")

    if not problems:
        if source != image_dir:
            problems.append(
                f"chart copies from {source} but the image ships manifests to {image_dir}"
            )
        if destination != env_value:
            problems.append(
                f"chart copies to {destination} but the operator reads {env_value}"
            )

    return problems, (image_dir, source, destination, env_value)


def main():
    parser = argparse.ArgumentParser()
    sub = parser.add_subparsers(dest="command", required=True)

    dup = sub.add_parser("duplicate-env")
    dup.add_argument("files", nargs="+")

    contract = sub.add_parser("manifest-path-contract")
    contract.add_argument("containerfile")
    contract.add_argument("chart_deployment")

    rbac = sub.add_parser("rbac-subjects")
    rbac.add_argument("files", nargs="+")

    args = parser.parse_args()

    if args.command == "duplicate-env":
        failures = duplicate_env(args.files)
        for failure in failures:
            print(failure, file=sys.stderr)
        return 1 if failures else 0

    if args.command == "rbac-subjects":
        failures = rbac_subjects(args.files)
        for failure in failures:
            print(failure, file=sys.stderr)
        return 1 if failures else 0

    problems, paths = manifest_path_contract(args.containerfile, args.chart_deployment)
    image_dir, source, destination, env_value = paths
    print(f"image ships manifests to : {image_dir}")
    print(f"chart copies from        : {source}")
    print(f"chart copies to          : {destination}")
    print(f"operator reads           : {env_value}")
    for problem in problems:
        print(problem, file=sys.stderr)
    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main())
