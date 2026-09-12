Status: ready-for-agent

# ANI-RSS Go 后端渐进替换

## Problem Statement

ANI-RSS 当前后端是一个 Spring Boot Java 单体应用。它能够完成番剧订阅、资源源站抓取、下载器调度、刮削、媒体整理、通知和 Web UI 服务，但运行时依赖 Java，后端资源占用、启动速度、单文件分发和跨平台发布仍有改进空间。

项目希望在不改变现有 UI 的前提下，用 Go 逐步替换 Java 后端。迁移不能中断现有使用，也不能因为两个后端并行运行而造成重复抓取、重复下载、重复重命名或数据损坏。最终 Java 必须可以移除，Go 独立提供当前最新版本的全链路功能。

## Solution

构建一个 Go 模块化单体后端，并在迁移期间提供 Go Gateway。Gateway 继续占用现有对外入口，已迁移的请求交给 Go，未迁移的请求转发给 Java 过渡后端，因此现有 Vue UI 不需要修改。

迁移按业务域推进，而不是按 Java 类逐个翻译。迁移期间只有一套 RSS 和重命名定时任务、一个明确的数据写入者以及一个明确的任务协调者。初期 Go 通过存储抽象接入现有 JSON 数据，完成迁移后把配置、订阅、任务状态和缓存统一存入 SQLite，JSON 只用于导入导出，ZIP 用于备份。

Go 后端必须保持当前 UI 所依赖的 HTTP API 契约，包括路径、HTTP 方法、参数名、请求体、返回 JSON 字段、认证方式、Cookie/Token 行为和错误响应约定。当前 UI、Emby Webhook、ICS、API Key、MCP、Swagger、文件、上传和下载日志等外部入口都属于最终功能范围。

## User Stories

1. As an ANI-RSS user, I want to use the existing UI without changing or relearning it, so that the backend rewrite is invisible to me.
2. As an ANI-RSS user, I want the existing API paths and HTTP methods to keep working, so that the current UI continues to operate unchanged.
3. As an ANI-RSS user, I want existing request parameters and JSON field names to remain available, so that every current page can communicate with the Go backend.
4. As an ANI-RSS user, I want successful responses to retain the current result shape and status semantics, so that the UI handles them as before.
5. As an ANI-RSS user, I want validation and error responses to retain the current observable contract, so that failures are displayed and handled consistently.
6. As an administrator, I want the Go Gateway to forward not-yet-migrated requests to Java, so that migration can happen without a big-bang cutover.
7. As an administrator, I want to switch an individual business domain back to Java during migration, so that a regression can be rolled back without reverting the whole deployment.
8. As an administrator, I want Java to be removable after migration, so that the final deployment has one backend and one source of truth.
9. As an administrator, I want only one process to execute RSS scheduling, so that a migration deployment cannot submit duplicate download tasks.
10. As an administrator, I want only one process to execute renaming and media organization, so that files cannot be moved or renamed twice.
11. As an administrator, I want only one process to write application state at a time, so that configuration, subscriptions, caches and task state cannot be corrupted.
12. As an administrator, I want the service to expose the existing health/ping behavior, so that deployment systems can determine whether it is ready.
13. As an administrator, I want to configure the service through the current configuration UI, so that I can continue managing behavior without editing implementation files.
14. As an administrator, I want configuration changes to be validated and normalized, so that URLs, paths, templates and defaults remain usable across platforms.
15. As an administrator, I want the Go service to import the current JSON configuration and subscription data, so that migration does not require rebuilding my subscriptions manually.
16. As an administrator, I want configuration export and import to remain available, so that I can move or restore an installation.
17. As an administrator, I want scheduled and manual backups to remain available, so that application state can be recovered after a failure.
18. As an administrator, I want the final storage to use SQLite, so that configuration, subscriptions, caches and task state have a consistent local data source.
19. As an administrator, I want JSON to remain an import/export format rather than a live multi-file database, so that external backup and migration workflows remain possible without coupling business logic to file layout.
20. As an administrator, I want to configure a resource source and RSS refresh interval, so that the service can follow the sources I use.
21. As an administrator, I want RSS feeds to be fetched with configured timeouts, proxy rules and retries, so that temporary network failures do not stop the entire refresh cycle.
22. As an administrator, I want RSS entries to be parsed into resources with title, episode, size, publication time, download address and source information, so that matching decisions are consistent.
23. As a subscriber, I want to add a new anime subscription, so that future matching resources can be processed automatically.
24. As a subscriber, I want to edit a subscription, so that source, subtitle group, season, naming and download rules can change over time.
25. As a subscriber, I want to delete a subscription with an optional file cleanup, so that I can remove tracking without accidentally deleting media unless I choose it.
26. As a subscriber, I want to list all subscriptions with their current progress and status, so that I can manage my watch list from the existing UI.
27. As a subscriber, I want to enable or disable subscriptions in bulk, so that seasonal changes can be managed efficiently.
28. As a subscriber, I want to refresh one subscription, so that I can fetch current resources without waiting for the scheduled cycle.
29. As a subscriber, I want to refresh all subscriptions, so that I can manually start a complete update.
30. As a subscriber, I want to preview how a subscription will be interpreted, so that I can correct matching rules before downloads start.
31. As a subscriber, I want to convert a resource RSS entry into a subscription, so that I can quickly follow a source discovered through the UI.
32. As a subscriber, I want to import a list of subscriptions, so that I can restore or bulk-configure my watch list.
33. As a subscriber, I want to set a per-subscription download path, so that different anime can be organized into different media libraries.
34. As a subscriber, I want to set a per-subscription episode offset, so that sources with nonstandard episode numbering can be matched correctly.
35. As a subscriber, I want to set include, exclude, priority, coexistence and delayed-download rules, so that the selected resource matches my preferences.
36. As a subscriber, I want the service to distinguish a main resource from backup resources, so that it can fall back when the preferred resource is unavailable.
37. As a subscriber, I want wash-version behavior to replace lower-priority media when appropriate, so that my library can improve without manual cleanup.
38. As a subscriber, I want missing episodes and total episode counts to be tracked, so that the UI shows meaningful progress.
39. As a subscriber, I want total episode counts to be updated manually or in batch, so that incomplete metadata can be corrected.
40. As a subscriber, I want Mikan search and seasonal listing to remain available, so that I can find anime and choose a resource source.
41. As a subscriber, I want Mikan resource groups and RSS links to remain available, so that I can select a subtitle group.
42. As a subscriber, I want AniBT seasonal search and group listing to remain available, so that I can use AniBT as a resource source.
43. As a subscriber, I want AnimeGarden listing and group listing to remain available, so that I can use AnimeGarden as a resource source.
44. As a subscriber, I want Bangumi title lookup and subject conversion to remain available, so that a selected subject can become an anime subscription.
45. As a subscriber, I want Bangumi rating and account-related operations to remain available, so that current metadata workflows continue to work.
46. As a subscriber, I want TMDB title and episode-group lookup to remain available, so that media metadata can be selected from the UI.
47. As a subscriber, I want metadata scraping to populate titles, covers, ratings, seasons and episode information, so that the media library is organized correctly.
48. As a subscriber, I want batch scraping and cover refresh to remain available, so that a library can be corrected without editing each item manually.
49. As a subscriber, I want NFO generation to remain available, so that media servers can identify organized content.
50. As a subscriber, I want qBittorrent integration to remain available, so that resources can be submitted and managed through qBittorrent.
51. As a subscriber, I want Transmission integration to remain available, so that I can use Transmission instead of qBittorrent.
52. As a subscriber, I want Aria2 integration to remain available, so that I can use Aria2 for resource downloads.
53. As a subscriber, I want OpenList integration to remain available, so that offline or remote download workflows continue to work.
54. As an administrator, I want downloader login tests to remain available, so that incorrect downloader settings can be detected before refreshes run.
55. As a subscriber, I want resources to be submitted as torrent files, magnet links or supported remote addresses, so that current source formats remain usable.
56. As a subscriber, I want download tasks to be queried and classified by status, so that processing waits for the right lifecycle state.
57. As a subscriber, I want completed download tasks to be renamed or moved according to the configured media rules, so that the final media library has predictable structure.
58. As a subscriber, I want torrent deletion to honor the selected file-deletion behavior, so that cleanup is explicit and safe.
59. As a subscriber, I want tracker updates to remain available, so that downloaders can use the configured tracker sources.
60. As a subscriber, I want collection preview and collection download to remain available, so that multi-episode resources can be processed as one operation.
61. As a subscriber, I want subtitle matching to remain available, so that subtitles can be associated with the right video.
62. As a subscriber, I want supported video, subtitle and image files to be recognized consistently, so that unrelated files are not processed as media.
63. As a subscriber, I want configurable naming templates and filename length limits, so that the resulting paths fit my media server and operating system.
64. As a subscriber, I want season, episode, subtitle group, quality and language information to be represented in names, so that files are understandable outside the application.
65. As a subscriber, I want media files to be scanned and listed through the play interface, so that I can inspect available videos from the UI.
66. As a subscriber, I want the service to serve authorized media files with appropriate content types, so that supported videos and subtitles can be played or downloaded.
67. As an administrator, I want proxy settings and proxy host rules to remain available, so that blocked external services can be reached selectively.
68. As an administrator, I want login protection, API Key access and IP whitelist behavior to remain available, so that exposing the service does not remove existing access controls.
69. As an administrator, I want login attempt limits and session/token expiry behavior to remain available, so that authentication remains safe.
70. As an administrator, I want logs to be viewed, cleared and downloaded through the UI, so that I can diagnose resource and download problems.
71. As an administrator, I want log levels and retention settings to remain configurable, so that routine operation does not consume unbounded storage.
72. As an administrator, I want Telegram notifications to remain available, so that resource and download events can reach my chats.
73. As an administrator, I want Bark notifications to remain available, so that events can reach Apple devices through Bark.
74. As an administrator, I want email notifications to remain available, so that events can be delivered through SMTP.
75. As an administrator, I want generic Webhook notifications to remain available, so that events can be integrated with other systems.
76. As an administrator, I want file-move notifications to remain available, so that completed media can be placed in another library automatically.
77. As an administrator, I want OpenList upload notifications to remain available, so that completed media can be uploaded and optionally removed locally.
78. As an administrator, I want Emby refresh notifications to remain available, so that a media server can rescan a library after organization.
79. As an administrator, I want shell notifications to remain available, so that custom local automation can run after selected events.
80. As an administrator, I want notification retries and event filters to remain available, so that transient failures do not silently lose important events.
81. As an Emby administrator, I want the Emby Webhook endpoint to remain available, so that external playback or library events can trigger existing behavior.
82. As an administrator, I want the ICS calendar endpoint to remain available, so that subscribed anime schedules can be consumed by calendar applications.
83. As an administrator, I want API Key-authenticated external URLs to remain available, so that automation can access calendar, webhook and other supported endpoints.
84. As an administrator, I want Swagger/OpenAPI documentation to remain available, so that the HTTP interface can be inspected and integrated.
85. As an MCP client, I want the MCP endpoint and current tools to remain available, so that an AI client can inspect and operate ANI-RSS.
86. As an administrator, I want the custom UI JavaScript and CSS behavior to remain available, so that installation-specific UI customization is not lost.
87. As an administrator, I want Web UI upload, update and delete behavior to remain available, so that custom UI assets can continue to be managed.
88. As an administrator, I want the service to start on Linux, Windows and macOS, so that the deployment options remain unchanged.
89. As an administrator, I want Docker images for amd64, arm64 and armv7, so that existing NAS and server deployments remain supported.
90. As an administrator, I want native binaries and platform packages to be built through cross-compilation and native CI packaging, so that releases remain reproducible across supported platforms.
91. As a macOS user, I want the system tray behavior, application bundle and DMG workflow to remain available, so that the desktop experience is preserved.
92. As a Windows user, I want the executable, startup behavior and automatic update workflow to remain available, so that the desktop installation remains usable.
93. As a Linux user, I want the current launcher, update and service workflow to remain available, so that existing installations can be upgraded normally.
94. As an administrator, I want the final Go binary to run without a Java runtime, so that deployment is smaller and simpler.
95. As an administrator, I want the Go backend to start quickly and use fewer idle resources than the Spring Boot backend, so that it is more suitable for small servers and NAS devices.
96. As a maintainer, I want full-chain tests to run through the HTTP boundary, so that tests validate behavior users and integrations can observe.
97. As a maintainer, I want fake resource sources, metadata services and downloaders in tests, so that tests are deterministic and do not depend on live third-party services.
98. As a maintainer, I want temporary media libraries in tests, so that file scanning, matching, renaming and cleanup can be verified safely.
99. As a maintainer, I want Java and Go to produce comparable results for migration scenarios, so that a domain can be switched with evidence rather than guesswork.
100. As a maintainer, I want Go-only end-to-end tests to pass before Java is removed, so that the final deployment has verified feature parity.

## Implementation Decisions

- The final backend is a Go modular monolith. The project will not be split into permanent microservices as part of this migration.
- During migration, a Go Gateway owns the existing public entry point. It routes migrated business domains to Go and forwards the remaining requests to a Java transition backend.
- The migration is by business domain: HTTP gateway and static resources first, then read-only/external integrations, resource sources, downloader adapters, subscription and RSS processing, media processing, notifications, storage, and platform packaging.
- Java is a temporary migration dependency, not a permanent service. The migration is complete only when Java can be shut down and removed without reducing the current latest-version feature set.
- The existing Vue UI is not modified for this project. Its API calls define the public contract that the Go backend must satisfy.
- The public HTTP contract preserves current paths, methods, query/body parameter names, JSON field names, result envelopes, authentication mechanisms, status semantics and externally consumed endpoints. The implementation may reorganize all internal code.
- The final Go service must provide all current controller capabilities, including configuration, subscriptions, resource-source search, RSS refresh, metadata lookup, scraping, downloaders, collection handling, torrent management, playback/file access, logs, uploads, notifications, Emby, ICS, API Key access, MCP and Swagger.
- The core domain modules are resource sources, subscription management, resource matching, download task coordination, downloader adapters, media processing, metadata scraping, notifications, configuration, authentication, storage, HTTP delivery and platform integration.
- Downloader integrations are represented behind a common adapter boundary covering login/test, submit, list/status, delete, rename, tags, tracker update and save-path operations. Each supported downloader keeps its protocol-specific behavior behind that boundary.
- Resource sources and metadata services are represented behind external-client boundaries so endpoint URLs, credentials, proxy selection, timeout and retry policy are not spread through domain logic.
- Task coordination must make ownership explicit. RSS refresh, download polling, rename processing and scheduled maintenance cannot be active in both Java and Go at the same time.
- The initial migration storage adapter reads and writes the existing JSON representation only where needed to bridge Java-owned state. Go business logic must access state through storage interfaces rather than directly depending on JSON files.
- The final storage is SQLite, with configuration, subscriptions, resource history/cache, task state, notification configuration and relevant operational state represented in a consistent local database. JSON remains an import/export representation; ZIP remains a backup container.
- The JSON-to-SQLite migration is explicit and repeatable enough to report validation failures. It must preserve user-visible subscriptions and configuration values required by the current UI, while old-version compatibility beyond the migration input is not a goal.
- The recommended Go implementation uses `net/http` with a small routing layer, `encoding/json` for the public JSON contract, structured standard-library logging, context-aware cancellation, bounded worker pools and a cron scheduler. HTML parsing, RSS parsing, SQLite access, ICS generation, torrent metainfo and desktop integration may use focused libraries behind narrow interfaces.
- Static UI assets are served from the existing build output and may be embedded in the Go binary, while the configured Web UI override remains available. Docker and native packaging must not require Java.
- Cross-compilation is used for Go binaries. Windows and macOS tray, application-bundle, DMG and installer behavior are verified and packaged on corresponding native CI runners; cross-compilation alone is not treated as proof of desktop integration.
- The public service keeps the current default port and external address behavior unless a deployment-specific packaging constraint requires an explicitly documented override.
- Error handling is defined at the HTTP boundary first: domain failures, external-service failures, authentication failures and validation failures must map to stable user-observable responses and structured logs.
- Security-sensitive behavior includes password hashing/verification, session or token lifecycle, API Key checks, login attempt limits, IP whitelist and reverse-proxy trust rules, path authorization, upload limits and safe shell/process execution.
- The implementation favors deterministic behavior around resource identity, episode matching, duplicate detection, main/backup resource selection, wash-version rules and file rename operations. File operations must be recoverable or fail without silently losing media.
- No business domain is permanently owned by both runtimes. During a cutover, one runtime is the active owner and the Gateway has a single routing decision for that domain.

## Testing Decisions

- The primary test seam is the existing HTTP boundary. Tests start the Go service or Gateway, issue requests in the same shape as the current UI and assert only externally observable behavior.
- Contract tests cover all current UI routes and external routes, including HTTP method, path matching, query/form/body decoding, authentication, status, result envelope, JSON fields, content type, cache headers, file streaming and multipart upload behavior.
- Migration routing tests verify that a migrated route reaches Go, an unmigrated route reaches Java, headers/cookies/authentication survive forwarding, and a route can be rolled back without changing the UI request.
- Differential migration tests run the same scenario against Java and Go where both implementations are available, comparing stable user-visible results while allowing implementation-specific timestamps, generated identifiers and log text to differ.
- Main-chain end-to-end tests use local fake HTTP services for resource sources, metadata services and downloaders. A scenario should be able to drive resource discovery, subscription matching, task submission, status polling, completion, media organization and notification triggering without live network access.
- File-processing tests use isolated temporary media libraries and verify only resulting files, paths, names, associations and cleanup behavior. They cover video/subtitle recognition, subtitle matching, episode extraction, naming templates, path normalization, duplicate handling, wash-version behavior and failure recovery.
- Downloader adapter tests use protocol-specific fake servers and cover login, submission, status mapping, deletion, rename, tags, tracker updates, save paths, retries and authentication/session details for qBittorrent, Transmission, Aria2 and OpenList.
- Resource-source tests use fixture responses and fake servers to cover RSS parsing, HTML parsing, seasonal/search results, group extraction, malformed responses, redirects, timeouts, proxy selection and source-specific filtering.
- Storage tests verify JSON import, SQLite persistence, restart recovery, transactional updates, concurrent access rules, backup/export and migration validation. Tests must not assert private table layout unless the schema is itself part of a public migration guarantee.
- Authentication and security tests cover valid and invalid credentials, hashed login input, token/session expiry, API Key access, IP whitelist behavior, trusted reverse-proxy handling, path traversal attempts, upload limits and shell notification argument handling.
- Notification tests use fake HTTP, SMTP and process boundaries. They verify event filtering, template rendering, retry behavior, failure reporting and that one event does not trigger duplicate delivery.
- ICS, Emby Webhook, MCP, Swagger, file access, logs and upload endpoints each receive black-box tests for their public protocol and authorization behavior.
- Platform tests build and smoke-test Linux amd64/arm64/armv7 artifacts, Windows artifacts and macOS artifacts. Desktop tray/launcher/DMG behavior is verified on native platform runners, while server behavior is tested independently of desktop UI availability.
- Performance checks compare cold start, idle memory, request latency for local endpoints and RSS/task throughput against the current Java baseline. They are acceptance evidence for the intended operational benefits, not substitutes for correctness tests.
- Existing tests provide only limited prior art: the current project has Spring Boot context and utility/integration-style tests but lacks comprehensive contract and end-to-end coverage. The Go test suite should establish HTTP contract and fake-boundary tests as the new baseline.
- Tests must not depend on live Mikan, TMDB, Bangumi, AniBT, AnimeGarden, downloader, Telegram, Emby or other third-party availability. Live smoke tests may exist separately and must not gate deterministic correctness tests.

## Out of Scope

- Changing the Vue UI's visual design, routes, interaction model or user-facing terminology.
- Maintaining compatibility with arbitrary historical Java APIs, old clients or every historical configuration field after the explicit migration input has been imported.
- Keeping Java as a permanent runtime or splitting the final product into Java and Go services.
- Replacing the current product with a cloud-hosted multi-user service.
- Introducing a separate PostgreSQL, MySQL or other database service for the final single-node deployment.
- Supporting new resource sources, downloaders, notification providers or media-server integrations that are not part of the current latest feature set.
- Requiring a live third-party service in the deterministic test suite.
- Optimizing network, downloader or disk-bound work under the assumption that a language rewrite alone will provide an order-of-magnitude throughput improvement.
- Rewriting the UI solely to make the new backend easier to implement.

## Further Notes

The current backend is approximately 190 Java source files and 21,000 lines, with around 72 HTTP mappings and a large amount of behavior concentrated in RSS processing, download orchestration, filename parsing and media organization. The main migration risk is behavioral drift in those areas, not HTTP routing.

The first implementation ticket should establish the Go Gateway, public contract inventory, Java forwarding, ownership guards for scheduled tasks, fake HTTP test harness and a reproducible local run. Subsequent tickets should migrate one business domain at a time and leave the system runnable after each cutover.

The final release gate is: existing UI runs without modification; all current external interfaces are available; the RSS-to-resource-to-download-to-media-library chain passes end to end; all four downloaders and current notifications are verified; JSON data imports into SQLite; supported platform artifacts build and smoke-test; and Java can be disabled and removed.
