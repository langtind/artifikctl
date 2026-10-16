# artifikctl

A command line tool for the public procurements on [Artifik](https://app.artifik.no): search them, read one in full, and download its attachments. It also lists the procurements you have bid on or been invited to.

The output is Markdown, so it reads well in a terminal and when an AI agent relays it into a chat. Every data command also prints JSON.

> artifikctl is unofficial and not affiliated with Artifik. It reads the data of Artifik's own web client, so it can break when the service changes.

## Install

```bash
brew install langtind/tap/artifikctl
```

Or with Go:

```bash
go install github.com/langtind/artifikctl@latest
```

## Use

No login is needed to search, read and download:

```bash
artifikctl procurements renhold --open
artifikctl procurement <procurement-id>
artifikctl questions <procurement-id>
artifikctl download <procurement-id> --all --dir ./docs
```

`mine` lists your own procurements, and needs a login. Artifik logs in with an SMS code, so you log in once in a browser and the tool takes the login over:

```bash
playwright-cli open https://app.artifik.no/login --headed   # log in here
playwright-cli state-save state.json
artifikctl login --state-stdin < state.json && rm state.json
artifikctl mine
```

The login is kept in the system keyring and renews itself. It lasts 7 days after the last use, and `artifikctl status` says until when. Set `ARTIFIKCTL_TOKEN` to the value of the `artifik-session` cookie to use a login without the keyring.

## For agents

```bash
artifikctl install-skill   # copies the skill to ~/.claude/skills
artifikctl schema          # every command, flag and error kind as JSON
```

Add `--json` to a data command for one JSON document on stdout, and `--fields id,name` to keep only some fields. A failed command exits with a code that says why: 2 wrong call, 3 not found, 4 not logged in, 5 a file is there already, 6 rate limit, 7 service unavailable. With `--json` the error is a JSON document too.

## Develop

```bash
go build -o artifikctl .
make check
```

## License

MIT
