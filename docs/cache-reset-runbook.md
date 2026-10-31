# Application cache initialization

The competition manual requires added caches to be initialized across regions. `/api/initialize` now waits for authenticated cache resets on every configured AP after SQL reset and image store wipe. SQL reset explicitly uses `remote_apply`; IP bans reload from the reset database. The guard service is unchanged.

For the current T68C topology, source the non-secret value in `etc-common/isureal/cache-reset.env` from `/home/isucon/env.sh` on all four hosts, using `export ISUREAL_CACHE_RESET_PEERS=...`. All eight services must inherit it. The existing private `ISUREAL_IMAGE_STORE_TOKEN` authenticates reset requests; never copy its value into this file or Git. The peer list intentionally includes the caller. The internal reset handler does not recursively contact peers.

After deployment, an official initialize must emit `cache reset complete` from all eight services. Any unreachable or rejecting peer makes initialization fail. Keep this initialization fix when reverting performance candidates.
