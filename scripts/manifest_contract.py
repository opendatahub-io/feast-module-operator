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
import os
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


def pod_spec(document):
    """The PodSpec of a workload, or None for other kinds."""
    kind = document.get("kind")
    if kind not in {"Deployment", "StatefulSet", "DaemonSet"}:
        return None
    spec = document.get("spec") or {}
    return ((spec.get("template") or {}).get("spec") or {})


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
        # A ServiceAccount is only unique per namespace: validation/manager and
        # system/manager are different identities. Key on both so a binding that
        # names the wrong namespace cannot pass on a name collision.
        service_accounts = set()
        for document in documents:
            if document.get("kind") != "ServiceAccount":
                continue
            metadata = document.get("metadata") or {}
            name = metadata.get("name")
            if name:
                service_accounts.add((metadata.get("namespace") or "", name))
        if not service_accounts:
            # Nothing to compare against; the chart may expect the platform to
            # create the identity.
            continue

        for document in documents:
            kind = document.get("kind")
            metadata = document.get("metadata") or {}
            name = metadata.get("name", "?")
            namespace = metadata.get("namespace") or ""
            if kind in {"RoleBinding", "ClusterRoleBinding"}:
                for subject in document.get("subjects") or []:
                    if subject.get("kind") != "ServiceAccount":
                        continue
                    account = subject.get("name")
                    # A namespaced subject without an explicit namespace
                    # resolves in the binding's own namespace.
                    account_namespace = subject.get("namespace") or namespace
                    if (account_namespace, account) not in service_accounts:
                        failures.append(
                            f"{path}: {kind}/{name} binds to ServiceAccount "
                            f"{account_namespace or '<cluster>'}/{account}, which the chart does not create"
                        )
            if kind in {"Deployment", "StatefulSet", "DaemonSet"}:
                account = (pod_spec(document) or {}).get("serviceAccountName")
                if account and (namespace, account) not in service_accounts:
                    failures.append(
                        f"{path}: {kind}/{name} runs as ServiceAccount "
                        f"{namespace or '<cluster>'}/{account}, which the chart does not create"
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


def normalize_mount_path(path):
    return (path or "").rstrip("/") or "/"


def parse_manifest_copy(argv):
    """Parse the supported recursive cp form used to populate the manifest volume."""
    if not argv or os.path.basename(argv[0]) != "cp":
        return None

    recursive = False
    options_done = False
    operands = []
    for arg in argv[1:]:
        if not options_done and arg == "--":
            options_done = True
            continue
        if not options_done and arg.startswith("-"):
            if arg in {"-r", "-R", "--recursive"}:
                recursive = True
                continue
            return None
        operands.append(arg)

    if not recursive or len(operands) != 2:
        return None

    source, destination = operands
    if not source.endswith("/.") or not destination.startswith("/"):
        return None
    return source[:-2].rstrip("/"), normalize_mount_path(destination)


def mount_for_path(container, path):
    """Return (volume name, normalized mount path) for a container mount."""
    wanted = normalize_mount_path(path)
    for mount in container.get("volumeMounts") or []:
        name = mount.get("name")
        mount_path = mount.get("mountPath")
        if name and mount_path and normalize_mount_path(mount_path) == wanted:
            return name, normalize_mount_path(mount_path)
    return None


def chart_manifest_paths(path):
    """One manifest-volume contract record per Deployment with a manager.

    A record per Deployment, not one shared set of variables. Overwriting a
    single set meant only the last Deployment reached the contract, so a second
    Deployment could hide a wrong path in the first - the RHOAIENG-99568
    failure class this check exists to catch.
    """
    found = []
    for document in load(path):
        spec = pod_spec(document)
        if spec is None:
            continue

        manager = None
        env_value = None
        # The operator's configuration is the manager container's. A sidecar
        # that also sets the variable does not change what the operator reads.
        for container in spec.get("containers") or []:
            if container.get("name") != "manager":
                continue
            manager = container
            for entry in container.get("env") or []:
                if entry.get("name") == "ODH_MODULE_OPERATOR_MANIFESTS_PATH":
                    env_value = entry.get("value")

        if manager is None:
            continue

        source = destination = None
        copier_mount = manager_mount = None
        # Only the copy-manifests init container populates the volume. `cp`
        # must be the executable: `command: [echo]` with "cp" in args copies
        # nothing even though it mentions every path.
        init_containers = spec.get("initContainers") or []
        copier = next(
            (c for c in init_containers if c.get("name") == "copy-manifests"),
            init_containers[0] if init_containers else None,
        )
        if copier is not None:
            argv = list(copier.get("command") or []) + list(copier.get("args") or [])
            parsed = parse_manifest_copy(argv)
            if parsed:
                source, destination = parsed
                copier_mount = mount_for_path(copier, destination)
                if env_value:
                    manager_mount = mount_for_path(manager, env_value)

        name = (document.get("metadata") or {}).get("name") or "<unnamed>"
        found.append(
            {
                "name": name,
                "source": source,
                "destination": destination,
                "env_value": env_value,
                "copier_mount": copier_mount,
                "manager_mount": manager_mount,
            }
        )
    return found


def manifest_path_contract(containerfile, chart_deployment):
    image_dir = containerfile_manifest_dir(containerfile)
    deployments = chart_manifest_paths(chart_deployment)

    problems = []
    if not image_dir:
        problems.append(f"{containerfile}: no COPY of config/manifests/ into an absolute path")
    if not deployments:
        problems.append(f"{chart_deployment}: no container named manager in a Deployment")

    for deployment in deployments:
        name = deployment["name"]
        source = deployment["source"]
        destination = deployment["destination"]
        env_value = deployment["env_value"]
        where = f"{chart_deployment}: Deployment {name}"
        if not source or not destination:
            problems.append(f"{where}: no supported recursive 'cp <dir>/. <dir>/' init container found")
            continue
        if not env_value:
            problems.append(
                f"{where}: the manager container does not set "
                "ODH_MODULE_OPERATOR_MANIFESTS_PATH"
            )
            continue
        if not deployment["copier_mount"]:
            problems.append(
                f"{where}: the copy-manifests init container does not mount {destination}"
            )
            continue
        if not deployment["manager_mount"]:
            problems.append(f"{where}: the manager container does not mount {env_value}")
            continue
        if deployment["copier_mount"] != deployment["manager_mount"]:
            copier_volume, copier_path = deployment["copier_mount"]
            manager_volume, manager_path = deployment["manager_mount"]
            problems.append(
                f"{where}: copy-manifests mounts {copier_volume} at {copier_path}, "
                f"but manager mounts {manager_volume} at {manager_path}; "
                "they must use the same volume and mount path"
            )
        if image_dir and source != image_dir:
            problems.append(
                f"{where}: chart copies from {source} but the image ships manifests to {image_dir}"
            )
        if destination != env_value:
            problems.append(
                f"{where}: chart copies to {destination} but the operator reads {env_value}"
            )

    return problems, image_dir, deployments


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

    problems, image_dir, deployments = manifest_path_contract(
        args.containerfile, args.chart_deployment
    )
    print(f"image ships manifests to : {image_dir}")
    for deployment in deployments:
        print(f"Deployment {deployment['name']}")
        print(f"  chart copies from      : {deployment['source']}")
        print(f"  chart copies to        : {deployment['destination']}")
        print(f"  operator reads         : {deployment['env_value']}")
    for problem in problems:
        print(problem, file=sys.stderr)
    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main())
