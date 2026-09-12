package ani.rss.service;

import ani.rss.commons.GsonStatic;
import ani.rss.util.basic.HttpReq;
import com.google.gson.JsonObject;
import lombok.extern.slf4j.Slf4j;
import org.springframework.stereotype.Service;

@Slf4j
@Service
public class CacheService {

    public JsonObject getBgmCover() {
        JsonObject jsonObject = new JsonObject();
        try {
            jsonObject = HttpReq.get("https://cache.wushuo.top/bgm/cover")
                    .thenFunction(res -> {
                        HttpReq.assertStatus(res);
                        return GsonStatic.fromJson(res.body(), JsonObject.class);
                    });
        } catch (Exception e) {
            log.error(e.getMessage(), e);
        }
        return jsonObject;
    }
}
