"""Offline workflow checks; run with an existing PyYAML environment.

Shell tests use temporary files and stub go/gh commands, never a real release.
"""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
import zipfile

import yaml


ROOT = Path(__file__).resolve().parents[1]
WORKFLOW = ROOT / ".github/workflows/release.yml"
SHA = "0123456789abcdef0123456789abcdef01234567"


class ReleaseWorkflowTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.text = WORKFLOW.read_text()
        cls.workflow = yaml.load(cls.text, Loader=yaml.BaseLoader)
        cls.build = cls.workflow["jobs"]["build"]
        cls.release = cls.workflow["jobs"]["release"]

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.cwd = Path(self.temp.name)
        self.env = dict(os.environ, VERSION="1.2.3", BUILD_SHA=SHA,
                        TAG_REF="refs/tags/v1.2.3", GH_TOKEN="test-token",
                        GH_REPO="example/voice_input",
                        GITHUB_SERVER_URL="https://github.com",
                        GITHUB_OUTPUT=str(self.cwd / "output"),
                        GH_LOG=str(self.cwd / "gh.jsonl"))
        (self.cwd / "output").touch()

    def step(self, job, name):
        return next(step for step in job["steps"] if step.get("name") == name)

    def run_step(self, job, name, cwd=None, **env):
        return subprocess.run(
            ["bash", "-e", "-o", "pipefail", "-c", self.step(job, name)["run"]],
            cwd=cwd or self.cwd, env=dict(self.env, **env),
            text=True, capture_output=True,
        )

    def stub(self, name, body):
        path = self.cwd / name
        path.write_text(body)
        path.chmod(0o755)
        self.env["PATH"] = str(self.cwd) + os.pathsep + self.env["PATH"]

    def test_yaml_policy_and_shell_syntax(self):
        workflow = self.workflow
        self.assertEqual(set(workflow["jobs"]), {"build", "release"})
        self.assertEqual(workflow["permissions"], {"contents": "read"})
        self.assertEqual(self.build["permissions"], {"contents": "read"})
        self.assertEqual(self.release["permissions"], {"contents": "write"})
        self.assertEqual(self.release["needs"], "build")
        self.assertEqual(self.release["if"],
                         "github.event_name == 'push' && startsWith(github.ref, 'refs/tags/v')")
        self.assertEqual(workflow["on"]["push"]["tags"], ["v*"])
        version = workflow["on"]["workflow_dispatch"]["inputs"]["version"]
        self.assertEqual(version["required"], "true")
        self.assertEqual(version["type"], "string")
        self.assertNotIn("default", version)
        self.assertEqual(workflow["concurrency"],
                         {"group": "release-${{ github.ref }}", "cancel-in-progress": "false"})
        self.assertEqual(self.build["env"]["VOICE_INPUT_REAL_ACCEPTANCE"], "")
        self.assertEqual(self.build["env"]["CGO_ENABLED"], "1")
        self.assertEqual(self.build["env"]["GOOS"], "linux")
        self.assertEqual(self.build["env"]["GOTOOLCHAIN"], "local")
        for job in workflow["jobs"].values():
            self.assertEqual(job["runs-on"], "ubuntu-latest")
            self.assertNotIn("strategy", job)
            for step in job["steps"]:
                if "run" in step:
                    self.assertNotIn("${{", step["run"])
                    checked = subprocess.run(["bash", "-n"], input=step["run"],
                                             text=True, capture_output=True)
                    self.assertEqual(checked.returncode, 0, checked.stderr)
                if step.get("uses", "").startswith("actions/checkout@"):
                    self.assertEqual(step["with"]["persist-credentials"], "false")
                if step.get("uses", "").startswith("actions/upload-artifact@"):
                    self.assertEqual(step["with"]["if-no-files-found"], "error")
                    self.assertNotIn("*", step["with"]["path"])
                    self.assertEqual(len(step["with"]["path"].splitlines()), 2)
                    if step["with"]["name"] == "source":
                        self.assertEqual(step["if"], "github.event_name == 'workflow_dispatch'")
        cross = self.step(self.build, "Cross-compile Windows x64")
        self.assertEqual(cross["env"]["CGO_ENABLED"], "0")
        self.assertEqual(cross["env"]["GOOS"], "windows")
        self.assertEqual(cross["env"]["GOARCH"], "amd64")
        self.assertIn("for package in wininput audio settings app popup instance startup; do", cross["run"])
        self.assertIn("go test -c", cross["run"])
        self.assertNotIn("always()", self.text)
        self.assertNotIn("arm", self.text.lower())

    def test_reads_toolchain_not_go_directive(self):
        (self.cwd / "go.mod").write_text("module fixture\n\ngo 1.25.0\ntoolchain go1.26.9\n")
        result = self.run_step(self.build, "Read Go toolchain")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual((self.cwd / "output").read_text(), "version=1.26.9\n")
        (self.cwd / "go.mod").write_text("module fixture\ngo 1.25.0\n")
        self.assertNotEqual(self.run_step(self.build, "Read Go toolchain").returncode, 0)

    def test_version_inputs_are_quoted_and_backend_failure_stops_build(self):
        self.stub("go", """#!/usr/bin/env bash
set -euo pipefail
printf '%s\\0' "$@" > "$GO_ARGS"
[[ "$#" -eq 5 && "$1" == run && "$2" == ./tools/winresource && "$3" == -version && "$5" == -print-version ]]
[[ "${MOCK_REJECT:-0}" == 0 ]]
printf '%s\\n' "$4"
""")
        self.stub("git", '#!/usr/bin/env bash\nprintf "%s\\n" "$BUILD_SHA"\n')
        self.env["GO_ARGS"] = str(self.cwd / "go-args")
        for event, ref, raw, reject in [
            ("push", "refs/tags/v1.2.3", "1.2.3", "0"),
            ("push", "refs/tags/v1.2.3-rc.1", "1.2.3-rc.1", "0"),
            ("workflow_dispatch", "refs/tags/v9.9.9", "1.2.3", "0"),
            ("push", "refs/tags/v1.2.3+build", "1.2.3+build", "1"),
            ("workflow_dispatch", "refs/heads/main", "", "1"),
            ("workflow_dispatch", "refs/heads/main", "1.2.3; touch injected", "1"),
        ]:
            with self.subTest(event=event, ref=ref, raw=raw):
                (self.cwd / "output").write_text("")
                result = self.run_step(self.build, "Validate version and record commit",
                                       EVENT_NAME=event, TAG_REF=ref, DISPATCH_VERSION=raw,
                                       MOCK_REJECT=reject)
                self.assertEqual(result.returncode == 0, reject == "0", result.stderr)
                args = (self.cwd / "go-args").read_bytes().split(b"\0")[:-1]
                self.assertEqual(args[3].decode(), raw)
                output = (self.cwd / "output").read_text()
                self.assertEqual(output, f"version={raw}\nsha={SHA}\n" if reject == "0" else "")
                self.assertFalse((self.cwd / "injected").exists())
        self.assertNotEqual(self.run_step(self.build, "Validate version and record commit",
                                         EVENT_NAME="push", TAG_REF="refs/heads/main").returncode, 0)

    def assets(self):
        directory = self.cwd / "release-assets"
        directory.mkdir()
        archive = directory / "voice_input-1.2.3-windows-amd64.zip"
        archive.write_bytes(b"fixture archive")
        checksum = directory / (archive.name + ".sha256")
        checksum.write_text(f"{hashlib.sha256(archive.read_bytes()).hexdigest()}  {archive.name}\n")
        return directory, archive, checksum

    def test_artifacts_require_exact_pair_and_matching_checksum(self):
        directory, archive, checksum = self.assets()
        name = "Verify release artifacts"
        self.assertEqual(self.run_step(self.release, name, cwd=directory).returncode, 0)
        extra = directory / ".unexpected"
        extra.touch()
        self.assertNotEqual(self.run_step(self.release, name, cwd=directory).returncode, 0)
        extra.unlink()
        other = self.cwd / "other-file"
        other.write_bytes(b"unrelated")
        valid_checksum = checksum.read_text()
        checksum.write_text(f"{hashlib.sha256(other.read_bytes()).hexdigest()}  ../other-file\n")
        self.assertNotEqual(self.run_step(self.release, name, cwd=directory).returncode, 0)
        checksum.write_text(valid_checksum)
        archive.write_bytes(b"changed")
        self.assertNotEqual(self.run_step(self.release, name, cwd=directory).returncode, 0)
        checksum.unlink()
        self.assertNotEqual(self.run_step(self.release, name, cwd=directory).returncode, 0)

    def install_gh_stub(self):
        self.stub("gh", f"#!{sys.executable}\n" + """import json, os, sys
args = sys.argv[1:]
with open(os.environ['GH_LOG'], 'a') as log:
    log.write(json.dumps(args) + '\\n')
if args[:2] == ['release', 'view']:
    sys.exit(0 if os.environ.get('EXISTS') == '1' else 1)
if args[0] == 'api':
    if os.environ.get('API_FAIL') == '1':
        sys.exit(1)
    kind = 'tag' if '/git/ref/' in args[1] and os.environ.get('ANNOTATED') == '1' else 'commit'
    sha = os.environ.get('REMOTE_SHA', os.environ['BUILD_SHA'])
    print(kind + '\\t' + sha)
elif args[:2] != ['release', 'create']:
    sys.exit(2)
""")

    def test_release_flags_remote_tag_and_no_overwrite(self):
        self.install_gh_stub()
        name = "Verify remote tag and create release"
        cases = [
            ({}, True),
            ({"VERSION": "1.2.3-rc.1", "TAG_REF": "refs/tags/v1.2.3-rc.1", "ANNOTATED": "1"}, True),
            ({"EXISTS": "1"}, False),
            ({"REMOTE_SHA": "f" * 40}, False),
            ({"API_FAIL": "1"}, False),
            ({"TAG_REF": "refs/tags/v9.9.9"}, False),
        ]
        for overrides, succeeds in cases:
            with self.subTest(overrides=overrides):
                Path(self.env["GH_LOG"]).write_text("")
                result = self.run_step(self.release, name, **overrides)
                self.assertEqual(result.returncode == 0, succeeds, result.stderr)
                calls = [json.loads(line) for line in Path(self.env["GH_LOG"]).read_text().splitlines()]
                creates = [args for args in calls if args[:2] == ["release", "create"]]
                self.assertEqual(len(creates), int(succeeds))
                if succeeds:
                    args = creates[0]
                    self.assertIn("--verify-tag", args)
                    self.assertEqual(args[3:5], [
                        f"release-assets/voice_input-{overrides.get('VERSION', '1.2.3')}-windows-amd64.zip",
                        f"release-assets/voice_input-{overrides.get('VERSION', '1.2.3')}-windows-amd64.zip.sha256",
                    ])
                    self.assertEqual("--prerelease" in args, "VERSION" in overrides)
                    self.assertEqual("--latest=false" in args, "VERSION" in overrides)
                    self.assertIn(SHA, (self.cwd / "release-notes.md").read_text())
                    if overrides.get("ANNOTATED") == "1":
                        self.assertTrue(any('/git/tags/' in args[1] for args in calls if args[0] == 'api'))

    def test_manual_source_is_archive_of_exact_commit(self):
        git_dir = subprocess.check_output(["git", "rev-parse", "--absolute-git-dir"], cwd=ROOT, text=True).strip()
        commit = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip()
        (self.cwd / "bin").mkdir()
        result = self.run_step(self.build, "Archive matching source for manual builds",
                               BUILD_SHA=commit, GIT_DIR=git_dir, GIT_WORK_TREE=str(ROOT))
        self.assertEqual(result.returncode, 0, result.stderr)
        archive = self.cwd / "bin/voice_input-1.2.3-source.zip"
        with zipfile.ZipFile(archive) as source:
            files = {name.removeprefix("voice_input-1.2.3/") for name in source.namelist() if not name.endswith('/')}
        tracked = set(subprocess.check_output(["git", "ls-tree", "-r", "--name-only", commit], cwd=ROOT, text=True).splitlines())
        self.assertEqual(files, tracked)
        self.assertFalse(any(".git" in Path(name).parts for name in files))
        verified = subprocess.run(["sha256sum", "--check", archive.name + ".sha256"],
                                  cwd=archive.parent, text=True, capture_output=True)
        self.assertEqual(verified.returncode, 0, verified.stderr)


if __name__ == "__main__":
    unittest.main(verbosity=2)
