package ani.rss.service;

import ani.rss.task.BaseTask;
import ani.rss.task.BgmTask;
import ani.rss.task.RenameTask;
import ani.rss.task.RssTask;
import cn.hutool.core.text.NamingCase;
import cn.hutool.core.thread.ThreadUtil;
import cn.hutool.extra.spring.SpringUtil;
import lombok.extern.slf4j.Slf4j;
import org.springframework.stereotype.Service;

import java.util.List;
import java.util.Vector;
import java.util.concurrent.atomic.AtomicBoolean;

@Slf4j
@Service
public class TaskService {
    public static final AtomicBoolean LOOP = new AtomicBoolean(false);
    public static final List<Thread> THREADS = new Vector<>();

    private final RuntimeOwnership runtimeOwnership = new RuntimeOwnership();

    public void stop() {
        LOOP.set(false);
        for (Thread thread : THREADS) {
            try {
                // 等待现有任务结束
                while (thread.isAlive()) {
                    thread.interrupt();
                    ThreadUtil.sleep(100);
                }
                thread.join();
            } catch (Exception e) {
                log.error(e.getMessage(), e);
            }
        }
        THREADS.clear();
        runtimeOwnership.releaseAll();
    }

    public void restart() {
        stop();
        start();
    }

    public void start() {
        if (LOOP.get() && !THREADS.isEmpty()) {
            log.warn("任务已经在运行中");
            return;
        }
        LOOP.set(true);

        List<TaskDefinition> definitions = List.of(
                new TaskDefinition(RenameTask.class, "rename"),
                new TaskDefinition(RssTask.class, "rss"),
                new TaskDefinition(BgmTask.class, "maintenance")
        );

        for (TaskDefinition definition : definitions) {
            if (!runtimeOwnership.acquire(definition.domain())) {
                continue;
            }
            Class<? extends BaseTask> aClass = definition.taskClass();
            BaseTask task = SpringUtil.getBean(aClass);
            String name = aClass.getSimpleName();
            String threadName = NamingCase.toKebabCase(name);
            THREADS.add(new Thread(() -> task.run(threadName, LOOP)));
        }
        if (THREADS.isEmpty()) {
            LOOP.set(false);
            log.warn("RSS、重命名和维护任务均已被其他进程占用，Java 任务不会启动");
            return;
        }
        for (Thread thread : THREADS) {
            thread.start();
        }
    }

    private record TaskDefinition(Class<? extends BaseTask> taskClass, String domain) {
    }
}
