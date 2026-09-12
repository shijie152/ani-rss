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

/**
 * Coordinates migration-time scheduler ownership with the Go runtime.
 *
 * <p>The lock files intentionally use the same names and JSON fields as the
 * Go ownership manager. File creation is atomic, so each scheduler domain has
 * one owner while the Gateway can migrate domains independently.</p>
 */
@Slf4j
public final class RuntimeOwnership {
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
            try {
                Files.createFile(path);
            } catch (FileAlreadyExistsException e) {
                log.warn("运行时任务域已被其他进程占用: {}", domain);
                return false;
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
            Files.deleteIfExists(path);
        } catch (IOException e) {
            log.warn("释放运行时任务所有权失败: {}", path, e);
        }
    }

    public synchronized void releaseAll() {
        for (String domain : acquired.keySet().toArray(String[]::new)) {
            release(domain);
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
}
