# ANI-RSS Go Gateway

The Gateway is the public HTTP entry point during the Java-to-Go migration.
It serves the built Vue UI, handles routes owned by Go, and forwards remaining
`/api` requests to the Java transition backend.

From the repository root, build the UI and run the Gateway with:

```sh
mvn -pl ani-rss-ui -am package -DskipTests
go run ./go-backend/cmd/ani-rss
```

Run the repeatable Gateway smoke tests, including an in-process fake Java
backend, with:

```sh
go test ./go-backend/internal/gateway
```

The default values are:

- public address: `:7789`
- Java backend: `http://127.0.0.1:7790`
- UI directory: `ani-rss-ui/dist`

Override them with `LISTEN_ADDR`, `JAVA_URL`, `UI_DIR`, or the corresponding
flags. During migration, only one runtime should own scheduled tasks and write
application state.
