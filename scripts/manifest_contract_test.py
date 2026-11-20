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

    def write_deployment(self, copier_command, manager_volume="manifests"):
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
                                        "value": "/opt/manifests",
                                    }
                                ],
                                "volumeMounts": [
                                    {"name": manager_volume, "mountPath": "/opt/manifests"}
                                ],
                            }
                        ],
                        "initContainers": [
                            {
                                "name": "copy-manifests",
                                "command": copier_command,
                                "volumeMounts": [
                                    {"name": "manifests", "mountPath": "/opt/manifests"}
                                ],
                            }
                        ],
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
        self.assertEqual(deployments[0]["copier_mount"], ("manifests", "/opt/manifests"))
        self.assertEqual(deployments[0]["manager_mount"], ("manifests", "/opt/manifests"))

    def test_init_and_manager_must_share_a_volume(self):
        chart = self.write_deployment(
            ["cp", "-r", "/manifests/.", "/opt/manifests/"],
            manager_volume="different-volume",
        )
        problems, _, _ = manifest_path_contract(self.containerfile, chart)

        self.assertTrue(
            any("they must use the same volume and mount path" in problem for problem in problems),
            problems,
        )

    def test_chart_records_mounts_for_contract(self):
        chart = self.write_deployment(["cp", "-r", "/manifests/.", "/opt/manifests/"])
        deployments = chart_manifest_paths(chart)

        self.assertEqual(len(deployments), 1)
        self.assertEqual(deployments[0]["source"], "/manifests")
        self.assertEqual(deployments[0]["destination"], "/opt/manifests")


if __name__ == "__main__":
    unittest.main()
