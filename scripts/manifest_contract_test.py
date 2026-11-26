#!/usr/bin/env python3

import os
import tempfile
import unittest

import yaml

from manifest_contract import (
    chart_manifest_paths,
    manifest_path_contract,
    parse_manifest_copy,
)


class ManifestContractTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.containerfile = os.path.join(self.tmp.name, "Containerfile")
        with open(self.containerfile, "w", encoding="utf-8") as handle:
            handle.write("COPY config/manifests/ /manifests/\n")

    def write_deployment(
        self,
        copier_command,
        manager_volume="manifests",
        *,
        copier_args=None,
        copier_mount=None,
        manager_mount=None,
        manager_env_value="/opt/manifests",
    ):
        copier = {
            "name": "copy-manifests",
            "volumeMounts": [
                {
                    "name": "manifests",
                    "mountPath": "/opt/manifests",
                    **(copier_mount or {}),
                }
            ],
        }
        if copier_command is not None:
            copier["command"] = copier_command
        if copier_args is not None:
            copier["args"] = copier_args
        manager_volume_mount = {"name": manager_volume, "mountPath": "/opt/manifests"}
        manager_volume_mount.update(manager_mount or {})

        document = {
            "apiVersion": "apps/v1",
            "kind": "Deployment",
            "metadata": {"name": "operator"},
            "spec": {
                "template": {
                    "spec": {
                        "containers": [
                            {
                                "name": "manager",
                                "env": [
                                    {
                                        "name": "ODH_MODULE_OPERATOR_MANIFESTS_PATH",
                                        "value": manager_env_value,
                                    }
                                ],
                                "volumeMounts": [manager_volume_mount],
                            }
                        ],
                        "initContainers": [copier],
                        "volumes": [{"name": "manifests", "emptyDir": {}}],
                    }
                }
            },
        }
        path = os.path.join(self.tmp.name, "deployment.yaml")
        with open(path, "w", encoding="utf-8") as handle:
            yaml.safe_dump(document, handle)
        return path

    def test_parse_manifest_copy_requires_recursive_two_operand_form(self):
        self.assertEqual(
            parse_manifest_copy(["cp", "-r", "/manifests/.", "/opt/manifests/"]),
            ("/manifests", "/opt/manifests"),
        )
        self.assertEqual(
            parse_manifest_copy(["cp", "--recursive", "/manifests/.", "/opt/manifests"]),
            ("/manifests", "/opt/manifests"),
        )
        self.assertIsNone(
            parse_manifest_copy(["cp", "/manifests/.", "/opt/manifests/"])
        )
        self.assertIsNone(
            parse_manifest_copy(["cp", "-r", "/manifests/.", "/opt/manifests/", "extra"])
        )
        self.assertIsNone(
            parse_manifest_copy(["cp", "-rf", "/manifests/.", "/opt/manifests/"])
        )

    def test_current_volume_contract_passes(self):
        chart = self.write_deployment(["cp", "-r", "/manifests/.", "/opt/manifests/"])
        problems, image_dir, deployments = manifest_path_contract(
            self.containerfile, chart
        )

        self.assertEqual(problems, [])
        self.assertEqual(image_dir, "/manifests")
        self.assertEqual(
            deployments[0]["copier_mount"],
            ("manifests", "/opt/manifests", "", False, ""),
        )
        self.assertEqual(
            deployments[0]["manager_mount"],
            ("manifests", "/opt/manifests", "", False, ""),
        )

    def test_init_and_manager_must_share_a_volume(self):
        chart = self.write_deployment(
            ["cp", "-r", "/manifests/.", "/opt/manifests/"],
            manager_volume="different-volume",
        )
        problems, _, _ = manifest_path_contract(self.containerfile, chart)

        self.assertTrue(
            any(
                "they must use the same volume, mount path, and subPath" in problem
                for problem in problems
            ),
            problems,
        )

    def test_init_command_is_required(self):
        chart = self.write_deployment(
            None,
            copier_args=["cp", "-r", "/manifests/.", "/opt/manifests/"],
        )
        problems, _, _ = manifest_path_contract(self.containerfile, chart)

        self.assertTrue(
            any("no supported recursive" in problem for problem in problems),
            problems,
        )

    def test_explicit_command_can_hold_operands_in_args(self):
        chart = self.write_deployment(
            ["cp"],
            copier_args=["-r", "/manifests/.", "/opt/manifests/"],
        )
        problems, _, _ = manifest_path_contract(self.containerfile, chart)

        self.assertEqual(problems, [])

    def test_copier_mount_must_be_writable(self):
        chart = self.write_deployment(
            ["cp", "-r", "/manifests/.", "/opt/manifests/"],
            copier_mount={"readOnly": True},
        )
        problems, _, _ = manifest_path_contract(self.containerfile, chart)

        self.assertTrue(
            any("read-only" in problem for problem in problems),
            problems,
        )

    def test_copier_and_manager_must_share_subpath(self):
        chart = self.write_deployment(
            ["cp", "-r", "/manifests/.", "/opt/manifests/"],
            copier_mount={"subPath": "writer"},
            manager_mount={"subPath": "reader"},
        )
        problems, _, _ = manifest_path_contract(self.containerfile, chart)

        self.assertTrue(
            any("same volume, mount path, and subPath" in problem for problem in problems),
            problems,
        )

    def test_manager_mount_may_be_read_only(self):
        chart = self.write_deployment(
            ["cp", "-r", "/manifests/.", "/opt/manifests/"],
            manager_mount={"readOnly": True},
        )
        problems, _, _ = manifest_path_contract(self.containerfile, chart)

        self.assertEqual(problems, [])

    def test_subpath_expr_is_rejected(self):
        chart = self.write_deployment(
            ["cp", "-r", "/manifests/.", "/opt/manifests/"],
            copier_mount={"subPathExpr": "$(POD_NAME)"},
        )
        problems, _, _ = manifest_path_contract(self.containerfile, chart)

        self.assertTrue(
            any("cannot be verified statically" in problem for problem in problems),
            problems,
        )

    def test_manifest_path_trailing_slash_is_normalized(self):
        chart = self.write_deployment(
            ["cp", "-r", "/manifests/.", "/opt/manifests/"],
            manager_env_value="/opt/manifests/",
        )
        problems, _, deployments = manifest_path_contract(self.containerfile, chart)

        self.assertEqual(problems, [])
        self.assertEqual(deployments[0]["env_value"], "/opt/manifests")

    def test_chart_records_mounts_for_contract(self):
        chart = self.write_deployment(["cp", "-r", "/manifests/.", "/opt/manifests/"])
        deployments = chart_manifest_paths(chart)

        self.assertEqual(len(deployments), 1)
        self.assertEqual(deployments[0]["source"], "/manifests")
        self.assertEqual(deployments[0]["destination"], "/opt/manifests")


if __name__ == "__main__":
    unittest.main()
