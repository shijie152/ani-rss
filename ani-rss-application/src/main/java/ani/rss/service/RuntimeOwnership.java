package ani.rss.service;

import ani.rss.util.other.ConfigUtil;
import lombok.extern.slf4j.Slf4j;

import java.io.IOException;
import java.nio.charset.StandardCharsets;
import java.nio.file.FileAlreadyExistsException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.StandardOpenOption;
import java.time.Instant;
import java.util.HashMap;
import java.util.Map;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/**
 * Coordinates migration-time scheduler ownership with the Go runtime.
 *
 * <p>The lock files intentionally use the same names and JSON fields as the
 * Go ownership manager. File creation is atomic, so each scheduler domain has
 * one owner while the Gateway can migrate domains independently.</p>
 */
@Slf4j
public final class RuntimeOwnership {
    private static final Pattern PID_PATTERN = Pattern.compile("\"pid\"\\s*:\\s*(\\d+)");
    private static final Pattern OWNER_PATTERN = Pattern.compile("\"owner\"\\s*:\\s*\"([^\"]+)\"");

    private final Path directory;
    private final Map<String, Path> acquired = new HashMap<>();

    public RuntimeOwnership() {
        this.directory = ConfigUtil.getConfigDir().toPath().resolve("locks");
    }

    /** Attempts to claim one scheduler domain without blocking startup. */
    public synchronized boolean acquire(String domain) {
        if (acquired.containsKey(domain)) {
            return true;
        }
        try {
            Files.createDirectories(directory);
            Path path = directory.resolve("runtime-" + safe(domain) + ".lock");
            for (int attempt = 0; attempt < 2; attempt++) {
                try {
                    Files.createFile(path);
                    break;
                } catch (FileAlreadyExistsException e) {
                    if (attempt == 0 && staleLock(path)) {
                        try {
                            Files.deleteIfExists(path);
                            continue;
                        } catch (IOException ignored) {
                            // A concurrent owner may have replaced or retained
                            // the lock; the next create attempt decides safely.
                        }
                    }
                    log.warn("运行时任务域已被其他进程占用: {}", domain);
                    return false;
                }
            }
            String payload = "{\"owner\":\"java\",\"pid\":" + ProcessHandle.current().pid()
                    + ",\"acquiredAt\":\"" + Instant.now() + "\"}\n";
            try {
                Files.writeString(path, payload, StandardCharsets.UTF_8,
                        StandardOpenOption.TRUNCATE_EXISTING, StandardOpenOption.WRITE);
            } catch (IOException e) {
                Files.deleteIfExists(path);
                throw e;
            }
            acquired.put(domain, path);
            return true;
        } catch (IOException e) {
            log.error("无法取得运行时任务所有权: {}", domain, e);
            return false;
        }
    }

    public synchronized void release(String domain) {
        Path path = acquired.remove(domain);
        if (path == null) {
            return;
        }
        try {
            if (ownsLock(path)) {
                Files.deleteIfExists(path);
            }
        } catch (IOException e) {
            log.warn("释放运行时任务所有权失败: {}", path, e);
        }
    }

    public synchronized void releaseAll() {
        for (String domain : acquired.keySet().toArray(String[]::new)) {
            release(domain);
        }
    }

    /**
     * Returns whether this Java process is the current state writer. A missing
     * lock is treated as the legacy single-runtime case so older standalone
     * Java launches keep their existing behaviour.
     */
    public static boolean javaOwnsState() {
        Path path = ConfigUtil.getConfigDir().toPath().resolve("locks/runtime-state.lock");
        if (!Files.exists(path)) {
            return true;
        }
        try {
            String payload = Files.readString(path, StandardCharsets.UTF_8);
            Matcher pid = PID_PATTERN.matcher(payload);
            Matcher owner = OWNER_PATTERN.matcher(payload);
            return pid.find() && owner.find()
                    && Long.parseLong(pid.group(1)) == ProcessHandle.current().pid()
                    && "java".equals(owner.group(1));
        } catch (Exception e) {
            return false;
        }
    }

    private static String safe(String value) {
        StringBuilder result = new StringBuilder(value.length());
        for (int i = 0; i < value.length(); i++) {
            char character = value.charAt(i);
            if (Character.isLetterOrDigit(character) || character == '-' || character == '_') {
                result.append(character);
            } else {
                result.append('_');
            }
        }
        return result.toString();
    }

    private static boolean staleLock(Path path) {
        try {
            String payload = Files.readString(path, StandardCharsets.UTF_8);
            Matcher matcher = PID_PATTERN.matcher(payload);
            if (!matcher.find()) {
                return false;
            }
            long pid = Long.parseLong(matcher.group(1));
            return ProcessHandle.of(pid).map(process -> !process.isAlive()).orElse(true);
        } catch (Exception e) {
            // An unreadable or malformed lock is left in place. Failing closed
            // is safer than guessing that another runtime has exited.
            return false;
        }
    }

    private boolean ownsLock(Path path) {
        try {
            String payload = Files.readString(path, StandardCharsets.UTF_8);
            Matcher matcher = PID_PATTERN.matcher(payload);
            if (!matcher.find()) {
                return false;
            }
            long pid = Long.parseLong(matcher.group(1));
            Matcher owner = OWNER_PATTERN.matcher(payload);
            return pid == ProcessHandle.current().pid()
                    && owner.find() && "java".equals(owner.group(1));
        } catch (Exception e) {
            return false;
        }
    }
}
