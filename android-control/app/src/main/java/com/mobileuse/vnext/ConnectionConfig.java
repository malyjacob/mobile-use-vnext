package com.mobileuse.vnext;

import org.json.JSONException;
import org.json.JSONObject;

import java.io.File;
import java.io.IOException;
import java.net.URI;
import java.net.URISyntaxException;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.NoSuchFileException;

/**
 * 私有 files/connection.json 的读取边界；不缓存配置，允许部署后立即重新配对。
 * 仅接受手机本机 HTTP 服务地址，避免把 Bearer 凭据发送到其他主机。
 * 任何解析错误只提供固定说明，不向界面或日志输出配置原文。
 */
final class ConnectionConfig {
    final URL syncUrl;
    final URL stopUrl;
    final String token;

    private ConnectionConfig(URL syncUrl, URL stopUrl, String token) {
        this.syncUrl = syncUrl;
        this.stopUrl = stopUrl;
        this.token = token;
    }

    /** 在后台线程读取；文件尚未部署时返回 null，其余读取或格式错误明确上报。 */
    static ConnectionConfig load(File file) throws ConfigException {
        final String contents;
        try {
            contents = new String(Files.readAllBytes(file.toPath()), StandardCharsets.UTF_8);
        } catch (NoSuchFileException missing) {
            return null;
        } catch (IOException error) {
            throw new ConfigException("无法读取私有连接配置，请检查部署结果。");
        }

        try {
            JSONObject json = new JSONObject(contents);
            URI baseUrl = new URI(json.getString("base_url"));
            String path = baseUrl.getPath();
            int port = baseUrl.getPort();
            if (!"http".equals(baseUrl.getScheme())
                    || !"127.0.0.1".equals(baseUrl.getHost())
                    || baseUrl.getUserInfo() != null
                    || baseUrl.getQuery() != null || baseUrl.getFragment() != null
                    || !(path.isEmpty() || "/".equals(path))
                    || !(port == -1 || (port > 0 && port <= 65535))) {
                throw new ConfigException("base_url 必须是 http://127.0.0.1[:端口]，不能包含路径、查询或凭据。");
            }
            String token = json.getString("token");
            if (token.isEmpty()) {
                throw new ConfigException("连接配置缺少有效 token，请重新部署连接凭据。");
            }
            // Bearer 值只能包含可见 ASCII；在文件边界拒绝换行等非法请求头字符。
            for (int i = 0; i < token.length(); i++) {
                if (token.charAt(i) <= 0x20 || token.charAt(i) >= 0x7f) {
                    throw new ConfigException("连接配置的 token 格式无效，请重新部署连接凭据。");
                }
            }
            return new ConnectionConfig(baseUrl.resolve("/v1/control/sync").toURL(),
                    baseUrl.resolve("/v1/control/stop").toURL(), token);
        } catch (JSONException | URISyntaxException error) {
            throw new ConfigException("连接配置格式错误，需要 base_url 和 token 两个字符串字段。");
        } catch (IOException error) {
            throw new ConfigException("连接配置的本机服务地址无效。");
        }
    }

    /** 服务错误也可能回显请求内容，显示前统一隐藏凭据。 */
    String redact(String text) {
        return text.replace(token, "[凭据已隐藏]");
    }

    static final class ConfigException extends Exception {
        ConfigException(String message) {
            super(message);
        }
    }
}
