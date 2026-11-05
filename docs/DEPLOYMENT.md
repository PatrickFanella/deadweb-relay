# Dozor deployment

Gitea CI runs the Go checks, backup archive tests and an isolated image smoke
check for pull requests. A verified push to `main` then deploys the exact commit
to the existing Dozor service at `https://www2.onnwee.me`. A manual dispatch can
retry the current commit, reusing an image already accepted by the host.

The app key can invoke only the host's pinned fleet receiver and Deadweb
profile. `HOMELAB_DEPLOY_KEY` and `HOMELAB_KNOWN_HOSTS` are repository secrets;
runtime credentials stay on Dozor. The operator owns key rotation and the host
profile. Pull requests cannot use the deployment steps.

Before replacing the relay image, the operator-installed copy of
`scripts/pre_deploy_backup.py` snapshots `/opt/deadweb-relay/data`. It briefly
pauses the existing container while streaming the Docker archive. A separate
user-systemd timer resumes that exact container if the backup process is killed.
The user manager must have lingering enabled. The copy is bounded to 256 MiB
and 20 seconds; failure stops deployment before image activation.

The hook validates the archive and restores it into an isolated container using
the candidate image, a separate volume, no network and temporary credentials.
It checks the original bytes, serving UID, configured site name, health and
authored reference document before allowing activation. It checks original
file prefixes again after startup, allowing normal journal appends. Failed
fixture cleanup is retried on the next hook invocation; fixtures are removed
only when their per-run ownership label matches.

Backups, manifests and receipts remain private under
`~/.local/state/fleet-deploy/deadweb-relay/backups/` on Dozor. These are local
rollback copies. Off-host disaster recovery and retention are separate operator
work. Diagnostics can contain private content and must stay on the host.

The receiver retains the previous image and records its rollback command. If
activation fails, it restores that image. It does not automatically overwrite
data with the backup. A data restore requires stopping the relay, preserving
the current data directory, reviewing the selected private receipt and
restoring its archive with UID 1000 before starting the compatible image.
The CI change performs no data-format migration or reseeding.

To qualify a newly installed backup script against the current live image, the
operator can run it with `--qualify-current`. The CI profile always invokes it
without arguments so that it tests the received image for the current `main`.
Use `--recover` to clean recorded interrupted fixtures and recover a recorded
pause without starting another backup.

After activation, the receiver checks image identity, runtime isolation, the
existing data mount and public readiness. CI compares the four public brand
exports with the source revision. Public guestbook, wall and note writes are
not exercised against production during deployment.
