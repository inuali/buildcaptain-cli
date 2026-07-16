# Publishing to Homebrew (one-time setup + every release)

Everything is automated with GoReleaser; you only do the GitHub setup once.

## One-time setup

1. **Create the CLI repo.** On GitHub, create a public repo `inuali/buildcaptain-cli`, then:

   ```sh
   cd /Users/loris/Code/buildcaptain-cli
   git remote add origin git@github.com:inuali/buildcaptain-cli.git
   git push -u origin main
   ```

2. **Create the tap repo.** In the `inuali` org, create a second public repo named exactly **`homebrew-tap`** (the `homebrew-` prefix is what makes `brew install inuali/tap/...` work). Initialize it with just a README. GoReleaser will write `Formula/buildcaptain.rb` into it automatically on every release.

3. **Create a token so the release job can push to the tap.**
   GitHub → Settings → Developer settings → Personal access tokens → Fine-grained tokens → Generate new token:
   - Resource owner: **inuali** (the org — not your personal account; the org must allow fine-grained PATs under Org Settings → Third-party Access → Personal access tokens)
   - Repository access: *Only select repositories* → `homebrew-tap`
   - Permissions: *Contents → Read and write*
   - Expiration: your call (you'll rotate it when it expires)

4. **Store the token as a secret.** In the `buildcaptain-cli` repo → Settings → Secrets and variables → Actions → *New repository secret*:
   - Name: `HOMEBREW_TAP_TOKEN`
   - Value: the token from step 3

## Every release

```sh
git tag v0.1.0
git push origin v0.1.0
```

The `Release` GitHub Action then:
1. runs the tests,
2. builds a universal macOS binary (arm64 + x86_64) with the version stamped in,
3. creates a GitHub Release with the tar.gz + checksums,
4. commits an updated `Formula/buildcaptain.rb` to `inuali/homebrew-tap`.

## Verify (as a user would)

```sh
brew install inuali/tap/buildcaptain
buildcaptain version   # prints 0.1.0
```

`brew upgrade buildcaptain` picks up future releases automatically.

## Notes

- Binaries downloaded by `brew`/`curl` don't get Gatekeeper's quarantine flag, so code-signing/notarizing the CLI is optional. If you want it later, add Developer ID signing with `quill` to the GoReleaser config.
- Once the tool has some traction (stars/downloads), you can submit the formula to `homebrew-core` so users can `brew install buildcaptain` without the tap.
