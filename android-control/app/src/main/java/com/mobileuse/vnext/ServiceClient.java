package com.mobileuse.vnext;

import org.json.JSONArray;
import org.json.JSONException;
import org.json.JSONObject;

import java.io.BufferedReader;
import java.io.IOException;
import java.io.InputStream;
import java.io.InputStreamReader;
import java.io.OutputStream;
import java.net.ConnectException;
import java.net.HttpURLConnection;
import java.net.SocketTimeoutException;
import java.time.Instant;
import java.time.format.DateTimeParseException;
import java.util.ArrayList;
import java.util.Collections;
import java.util.HashSet;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Set;
import java.util.UUID;
import java.util.regex.Pattern;

/**
 * 控制同步协议的 HTTP/JSON 边界：POST /v1/control/sync 与 POST /v1/control/stop。
 * 只使用系统 HttpURLConnection，无设备状态和调度职责；同步与停止可在独立线程并发，
 * 每次调用拥有自己的连接及不可变请求/响应。
 * 除脱敏后的固定说明外不向界面或日志暴露 token、原始响应体或未知服务字段。
 */
final class ServiceClient implements AutoCloseable {
    /** 共同普通契约；与服务端必须精确相等才能执行普通命令或声明模式就绪。 */
    static final String CONTRACT_VERSION = GeneratedVersion.CONTRACT_VERSION;
    static final String MODE_VIRTUAL = "virtual";
    static final String MODE_PRIMARY = "primary";
    static final String STOP_REASON_USER = "user_stop";
    static final String STOP_REASON_MODE_CHANGED = "mode_changed";
    // 显示命令集合固定；其他 kind 一律使整份响应作废。
    static final String COMMAND_CREATE_DISPLAY = "create_display";
    static final String COMMAND_CLOSE_DISPLAY = "close_display";
    /** 每拍响应最多携带的消息条数与请求最多携带的确认条数。 */
    static final int MAX_MESSAGES = 8;
    static final int MAX_MESSAGE_ACKS = 32;
    private static final Pattern MESSAGE_ID = Pattern.compile("M[1-9][0-9]{0,62}");
    private static final int TIMEOUT_MS = 3000;
    private final String instanceId;
    private final String appVersion;
    private final Set<HttpURLConnection> inFlight = new HashSet<>();
    private boolean closed; // 与 inFlight 一同受该集合的锁保护。

    ServiceClient(String instanceId, String appVersion) {
        this.instanceId = instanceId;
        this.appVersion = appVersion;
    }

    String appVersion() {
        return appVersion;
    }

    /** 调用者在主线程冻结本地状态，在同步线程执行本方法。 */
    SyncResponse sync(ConnectionConfig config, SyncRequest request) throws ServiceException {
        JSONObject body = request.toJson(instanceId, appVersion);
        Reply reply = post(config, config.syncUrl, body, true);
        // 旧服务会在解析普通请求时拒绝新增的必填成员：旧同步入口返回 400，旧严格接入层
        // 返回 422；404/405 同样说明该地址不接受本 App 的普通契约。统一进入版本不匹配视图，
        // 不回退旧字段、不重试旧请求。
        if (reply.statusCode == HttpURLConnection.HTTP_CONFLICT
                || reply.statusCode == HttpURLConnection.HTTP_BAD_REQUEST
                || reply.statusCode == HttpURLConnection.HTTP_NOT_FOUND
                || reply.statusCode == HttpURLConnection.HTTP_BAD_METHOD
                || reply.statusCode == 422) {
            String code = reply.statusCode == HttpURLConnection.HTTP_CONFLICT
                    ? "contract_mismatch" : "contract_rejected";
            throw new ServiceException("版本不匹配 · 设备操作不可用",
                    "服务不接受本 App 的普通契约（HTTP " + reply.statusCode
                            + "），请同时更新 App 与服务。", code);
        }
        return parseSync(requireOk(reply), config, reply.requestId);
    }

    /**
     * 独立停止线程调用。该端点不参与普通契约协商，因此即使同步因版本、鉴权或网络失败，
     * 只要服务可达就仍能绑定停止边界。
     */
    StopAck stop(ConnectionConfig config, long stopSeq, String reason) throws ServiceException {
        JSONObject body = new JSONObject();
        try {
            body.put("contract_version", CONTRACT_VERSION).put("app_instance_id", instanceId)
                    .put("stop_seq", stopSeq).put("reason", reason);
        } catch (JSONException error) {
            throw new ServiceException("停止编码失败", "停止请求无法编码。", "encode_failed");
        }
        return parseStop(requireOk(post(config, config.stopUrl, body, false)), config, stopSeq);
    }

    private JSONObject requireOk(Reply reply) throws ServiceException {
        if (reply.statusCode != HttpURLConnection.HTTP_OK) {
            throw httpError(reply.statusCode, reply.body, reply.config);
        }
        try {
            return new JSONObject(reply.body);
        } catch (JSONException error) {
            throw new ServiceException("响应格式错误", "服务响应不是合法 JSON，已忽略。", "invalid_response");
        }
    }

    private Reply post(ConnectionConfig config, java.net.URL url, JSONObject request, boolean sync)
            throws ServiceException {
        HttpURLConnection connection = null;
        String requestId = UUID.randomUUID().toString();
        long started = android.os.SystemClock.elapsedRealtime();
        String failureCode = null;
        try {
            byte[] body = request.toString().getBytes(java.nio.charset.StandardCharsets.UTF_8);
            connection = (HttpURLConnection) url.openConnection();
            synchronized (inFlight) {
                if (closed) {
                    throw new IOException("Client closed");
                }
                inFlight.add(connection);
            }
            connection.setRequestMethod("POST");
            connection.setConnectTimeout(TIMEOUT_MS);
            connection.setReadTimeout(TIMEOUT_MS);
            connection.setUseCaches(false);
            // 不携带 Authorization 跟随重定向到其他地址。
            connection.setInstanceFollowRedirects(false);
            connection.setRequestProperty("Authorization", "Bearer " + config.token);
            connection.setRequestProperty("Content-Type", "application/json; charset=utf-8");
            connection.setRequestProperty("Accept", "application/json");
            connection.setRequestProperty("X-Request-ID", requestId);
            connection.setDoOutput(true);
            connection.setFixedLengthStreamingMode(body.length);
            try (OutputStream output = connection.getOutputStream()) {
                output.write(body);
            }
            int statusCode = connection.getResponseCode();
            String response = statusCode == HttpURLConnection.HTTP_OK
                    ? readBody(connection.getInputStream()) : readBody(connection.getErrorStream());
            Reply reply = new Reply(statusCode, response, config, requestId);
            // 只有会读取业务正文的响应才计入本轮健康；错误码由调用方统一映射。
            if (statusCode == HttpURLConnection.HTTP_OK || statusCode == HttpURLConnection.HTTP_CONFLICT
                    || statusCode == HttpURLConnection.HTTP_UNAUTHORIZED) {
                failureCode = statusCode == HttpURLConnection.HTTP_OK ? null : "http_" + statusCode;
            } else {
                failureCode = "http_error";
            }
            return reply;
        } catch (SocketTimeoutException error) {
            failureCode = "timeout";
            throw new ServiceException("连接超时", "连接或读取本机服务超时（各 3 秒），稍后将自动重试。",
                    failureCode);
        } catch (ConnectException error) {
            failureCode = "connection_refused";
            throw new ServiceException("无法连接服务", "本机服务未接受连接，请确认手机上的服务已启动。",
                    failureCode);
        } catch (IOException error) {
            failureCode = "io_error";
            throw new ServiceException("网络请求失败", "无法完成本机 HTTP 请求，请检查服务后重试。",
                    failureCode);
        } finally {
            DiagnosticLog.network(sync, requestId, failureCode,
                    android.os.SystemClock.elapsedRealtime() - started);
            if (connection != null) {
                connection.disconnect();
                synchronized (inFlight) {
                    inFlight.remove(connection);
                }
            }
        }
    }

    /** 销毁时取消在途 I/O；已发送的请求仍不能视为服务端已撤销，迟到响应必须另外丢弃。 */
    @Override public void close() {
        List<HttpURLConnection> connections;
        synchronized (inFlight) {
            closed = true;
            connections = new ArrayList<>(inFlight);
        }
        for (HttpURLConnection connection : connections) {
            connection.disconnect();
        }
    }

    /**
     * 严格解析控制视图。缺字段、未知枚举或未知命令 kind 直接拒绝整份响应，
     * 不用 idle/ready 默认值补齐。
     */
    private static SyncResponse parseSync(JSONObject json, ConnectionConfig config, String requestId)
            throws ServiceException {
        try {
            requireContract(json);
            String serviceInstanceId = uuid(json, "service_instance_id");
            JSONObject control = json.getJSONObject("control");
            long revision = control.getLong("revision");
            if (revision < 0) {
                throw new IllegalArgumentException("negative revision");
            }
            Availability availability = availability(control.getJSONObject("availability"));
            Session session = control.isNull("session") ? null
                    : session(control.getJSONObject("session"));
            Work work = control.isNull("work") ? null : work(control.getJSONObject("work"));
            Cleanup cleanup = control.isNull("cleanup") ? null
                    : cleanup(control.getJSONObject("cleanup"));
            List<Command> commands = new ArrayList<>();
            JSONArray array = json.getJSONArray("commands");
            for (int i = 0; i < array.length(); i++) {
                commands.add(command(array.getJSONObject(i)));
            }
            // messages 是必填数组；缺失、非数组或元素形状错误都使整份响应作废。
            List<Message> messages = new ArrayList<>();
            JSONArray messageArray = json.getJSONArray("messages");
            if (messageArray.length() > MAX_MESSAGES) {
                throw new IllegalArgumentException("too many messages");
            }
            for (int i = 0; i < messageArray.length(); i++) {
                messages.add(message(messageArray.getJSONObject(i)));
            }
            StopState stop = json.isNull("stop") || !json.has("stop") ? null
                    : new StopState(json.getJSONObject("stop").getLong("seq"),
                            json.getJSONObject("stop").getString("state"));
            return new SyncResponse(serviceInstanceId, requestId,
                    new ControlView(revision, availability, session, work, cleanup),
                    Collections.unmodifiableList(commands), Collections.unmodifiableList(messages), stop);
        } catch (JSONException | IllegalArgumentException error) {
            // Instant/UUID/枚举解析错误同属外部响应错误，不回显原始输入。
            throw new ServiceException("响应格式错误", "服务返回的控制同步不符合共同契约版本 "
                    + CONTRACT_VERSION + "。", "invalid_response");
        }
    }

    private static StopAck parseStop(JSONObject json, ConnectionConfig config, long sentSeq)
            throws ServiceException {
        try {
            requireContract(json);
            String serviceInstanceId = uuid(json, "service_instance_id");
            boolean accepted = json.getBoolean("accepted");
            long seq = json.getLong("stop_seq");
            String state = json.getString("state");
            if (seq != sentSeq) {
                throw new IllegalArgumentException("stop seq mismatch");
            }
            if (accepted && !isStopState(state)) {
                throw new IllegalArgumentException("invalid stop state");
            }
            return new StopAck(serviceInstanceId, accepted, seq, state);
        } catch (JSONException | IllegalArgumentException error) {
            throw new ServiceException("停止响应格式错误", "服务返回的停止确认不符合共同契约版本 "
                    + CONTRACT_VERSION + "。", "invalid_stop_response");
        }
    }

    private static void requireContract(JSONObject json) throws JSONException {
        if (!CONTRACT_VERSION.equals(json.getString("contract_version"))) {
            throw new IllegalArgumentException("contract version mismatch");
        }
    }

    static boolean isStopState(String state) {
        return "revoked".equals(state) || "recorded".equals(state) || "duplicate".equals(state)
                || "accepted".equals(state);
    }

    private static Availability availability(JSONObject json) throws JSONException {
        String state = json.getString("state");
        if (!"ready".equals(state) && !"recovering".equals(state) && !"blocked".equals(state)) {
            throw new IllegalArgumentException("invalid availability state");
        }
        return new Availability(state, nullableString(json, "reason"));
    }

    private static Session session(JSONObject json) throws JSONException {
        String mode = json.getString("mode");
        if (!MODE_VIRTUAL.equals(mode) && !MODE_PRIMARY.equals(mode)) {
            throw new IllegalArgumentException("invalid session mode");
        }
        String state = json.getString("state");
        if (!"starting".equals(state) && !"active".equals(state) && !"ended".equals(state)) {
            throw new IllegalArgumentException("invalid session state");
        }
        return new Session(text(json, "origin"), nullableString(json, "agent"), mode, state,
                nullableString(json, "started_at"), nullableString(json, "expires_at"),
                nullableString(json, "ended_at"), nullableString(json, "reason"));
    }

    /** work 只保留 P、类别与阶段；不解析也不展示节点、坐标、输入正文或命令。 */
    private static Work work(JSONObject json) throws JSONException {
        return new Work(text(json, "operation_id"), text(json, "kind"), text(json, "phase"));
    }

    private static Cleanup cleanup(JSONObject json) throws JSONException {
        return new Cleanup(text(json, "state"), nullableString(json, "execution"),
                nullableString(json, "tasks"), nullableString(json, "recents"),
                nullableString(json, "display"));
    }

    /** 显示命令每拍至多一条；任一未知 kind 或缺字段都拒绝整份响应。 */
    private static Command command(JSONObject json) throws JSONException {
        String id = text(json, "id");
        String kind = json.getString("kind");
        if (COMMAND_CREATE_DISPLAY.equals(kind)) {
            int width = json.getInt("width");
            int height = json.getInt("height");
            int density = json.getInt("density_dpi");
            if (width <= 0 || height <= 0 || density <= 0) {
                throw new IllegalArgumentException("invalid display size");
            }
            return new Command(id, kind, width, height, density, null, null, null);
        }
        if (COMMAND_CLOSE_DISPLAY.equals(kind)) {
            String targetInstance = text(json, "target_app_instance_id");
            Integer targetDisplay = json.isNull("target_display_id") ? null
                    : (Integer) json.getInt("target_display_id");
            if (targetDisplay == null || targetDisplay <= 0) {
                throw new IllegalArgumentException("close_display requires target_display_id");
            }
            return new Command(id, kind, 0, 0, 0, targetInstance, targetDisplay,
                    text(json, "target_generation"));
        }
        throw new IllegalArgumentException("unknown command kind");
    }

    /** 消息投影；id/seq/时间形状不合法时拒绝整份响应。正文只交给 App 私有存储。 */
    private static Message message(JSONObject json) throws JSONException {
        String id = text(json, "id");
        if (!MESSAGE_ID.matcher(id).matches()) {
            throw new IllegalArgumentException("invalid message id");
        }
        long seq = json.getLong("seq");
        if (seq <= 0) {
            throw new IllegalArgumentException("invalid message seq");
        }
        String text = text(json, "text");
        String createdAt = text(json, "created_at");
        try {
            Instant.parse(createdAt);
        } catch (DateTimeParseException invalidTime) {
            throw new IllegalArgumentException("invalid message time");
        }
        return new Message(id, seq, text, createdAt);
    }

    private static String nullableString(JSONObject json, String key) throws JSONException {
        return json.isNull(key) || !json.has(key) ? null : json.getString(key);
    }

    private static String text(JSONObject json, String key) throws JSONException {
        String value = json.getString(key);
        if (value.isEmpty()) {
            throw new IllegalArgumentException("empty text field");
        }
        return value;
    }

    private static String uuid(JSONObject json, String key) throws JSONException {
        String value = json.getString(key);
        String canonical = UUID.fromString(value).toString();
        if (!canonical.equalsIgnoreCase(value)) {
            throw new IllegalArgumentException("Invalid UUID");
        }
        return canonical;
    }

    /**
     * HTTP 状态决定主要错误；即使错误体不是协议 JSON，也保留 401/409 的明确含义。
     * 错误体只用于识别受控 code；其他原文不进入界面文字或日志。
     */
    private static ServiceException httpError(int code, String body, ConnectionConfig config) {
        String title;
        String detail;
        if (code == HttpURLConnection.HTTP_UNAUTHORIZED) {
            title = "认证失败";
            detail = "请重新部署连接凭据（HTTP 401）。";
        } else if (code == HttpURLConnection.HTTP_CONFLICT) {
            title = "版本不匹配 · 设备操作不可用";
            detail = "服务与本 App 的共同契约版本不一致（HTTP 409），请同步更新 App 与服务。";
        } else {
            title = "服务请求失败";
            detail = "服务返回 HTTP " + code + "。";
        }
        String code2 = controlledErrorCode(body);
        if (code2 != null) {
            detail += "\n" + code2;
        }
        return new ServiceException(title, config.redact(detail), "http_" + code);
    }

    private static String controlledErrorCode(String body) {
        try {
            String code = new JSONObject(body).getJSONObject("error").getString("code");
            return DiagnosticLog.validCode(code) ? code : null;
        } catch (JSONException | IllegalArgumentException invalidErrorBody) {
            return null;
        }
    }

    private static String readBody(InputStream input) throws IOException {
        if (input == null) {
            return "";
        }
        try (BufferedReader reader = new BufferedReader(new InputStreamReader(input,
                java.nio.charset.StandardCharsets.UTF_8))) {
            StringBuilder body = new StringBuilder();
            char[] buffer = new char[2048];
            int count;
            while ((count = reader.read(buffer)) != -1) {
                body.append(buffer, 0, count);
            }
            return body.toString();
        }
    }

    /**
     * 主线程冻结的同步请求。results 是本次请求携带的回执；messageAcks 是已持久化消息的
     * 确认，最多 32 条且按 id 去重；stopSeq 仅在本地停止未确认时出现。
     * primaryDisplay 在 virtual 模式必有，display 只在 App 当前持有虚拟屏时出现。
     */
    static final class SyncRequest {
        final String selectedMode;
        final String modeGeneration;
        final boolean modeReady;
        final boolean stopReady;
        final boolean notificationsEnabled;
        final boolean backgroundRunning;
        final Boolean powerInteractive;
        final Boolean keyguardLocked;
        final VirtualDisplayHost.Descriptor primaryDisplay;
        final VirtualDisplayHost.Descriptor display;
        final List<CommandResult> results;
        final List<String> messageAcks;
        final Long stopSeq;
        final String stopReason;

        SyncRequest(String selectedMode, String modeGeneration, boolean modeReady, boolean stopReady,
                boolean notificationsEnabled, boolean backgroundRunning, Boolean powerInteractive,
                Boolean keyguardLocked, VirtualDisplayHost.Descriptor primaryDisplay,
                VirtualDisplayHost.Descriptor display, List<CommandResult> results,
                List<String> messageAcks, Long stopSeq, String stopReason) {
            this.selectedMode = selectedMode;
            this.modeGeneration = modeGeneration;
            this.modeReady = modeReady;
            this.stopReady = stopReady;
            this.notificationsEnabled = notificationsEnabled;
            this.backgroundRunning = backgroundRunning;
            this.powerInteractive = powerInteractive;
            this.keyguardLocked = keyguardLocked;
            this.primaryDisplay = primaryDisplay;
            this.display = display;
            this.results = Collections.unmodifiableList(new ArrayList<>(results));
            LinkedHashSet<String> uniqueAcks = new LinkedHashSet<>();
            if (messageAcks != null) {
                for (String id : messageAcks) {
                    if (id == null || !MESSAGE_ID.matcher(id).matches()) {
                        continue;
                    }
                    uniqueAcks.add(id);
                    if (uniqueAcks.size() == MAX_MESSAGE_ACKS) {
                        break;
                    }
                }
            }
            this.messageAcks = Collections.unmodifiableList(new ArrayList<>(uniqueAcks));
            this.stopSeq = stopSeq;
            this.stopReason = stopReason;
        }

        JSONObject toJson(String instanceId, String appVersion) throws ServiceException {
            try {
                JSONArray array = new JSONArray();
                for (CommandResult result : results) {
                    array.put(result.toJson());
                }
                JSONArray ackArray = new JSONArray();
                for (String id : messageAcks) {
                    ackArray.put(id);
                }
                return new JSONObject().put("contract_version", CONTRACT_VERSION)
                        .put("app_instance_id", instanceId).put("app_version", appVersion)
                        .put("selected_mode", selectedMode).put("mode_generation", modeGeneration)
                        .put("mode_ready", modeReady).put("stop_ready", stopReady)
                        .put("notifications_enabled", notificationsEnabled)
                        .put("background_running", backgroundRunning)
                        .put("power_interactive", powerInteractive == null
                                ? JSONObject.NULL : powerInteractive)
                        .put("keyguard_locked", keyguardLocked == null
                                ? JSONObject.NULL : keyguardLocked)
                        .put("primary_display", primaryDisplay == null
                                ? JSONObject.NULL : primaryDisplay.toJson())
                        .put("display", display == null ? JSONObject.NULL : display.toJson())
                        .put("command_results", array)
                        .put("message_acks", ackArray)
                        .put("stop_pending", stopSeq == null ? JSONObject.NULL
                                : new JSONObject().put("seq", stopSeq).put("reason", stopReason));
            } catch (JSONException error) {
                throw new ServiceException("同步编码失败", "本地状态无法编码为共同契约版本 "
                        + CONTRACT_VERSION + "。", "encode_failed");
            }
        }
    }

    static final class Command {
        final String id;
        final String kind;
        final int width;
        final int height;
        final int densityDpi;
        // 仅 close_display 使用；缺任一项时 App 拒绝执行，绝不回退为无目标释放。
        final String targetAppInstanceId;
        final Integer targetDisplayId;
        final String targetGeneration;

        Command(String id, String kind, int width, int height, int densityDpi,
                String targetAppInstanceId, Integer targetDisplayId, String targetGeneration) {
            this.id = id;
            this.kind = kind;
            this.width = width;
            this.height = height;
            this.densityDpi = densityDpi;
            this.targetAppInstanceId = targetAppInstanceId;
            this.targetDisplayId = targetDisplayId;
            this.targetGeneration = targetGeneration;
        }

        boolean createsDisplay() {
            return COMMAND_CREATE_DISPLAY.equals(kind);
        }
    }

    /** 回执只确认 App 本地处理结果，不证明物理资源已释放。 */
    static final class CommandResult {
        final String id;
        final boolean success;
        final String errorCode;

        CommandResult(String id, boolean success, String errorCode) {
            this.id = id;
            this.success = success;
            this.errorCode = errorCode;
        }

        JSONObject toJson() throws JSONException {
            return new JSONObject().put("command_id", id).put("ok", success)
                    .put("error_code", errorCode == null ? JSONObject.NULL : errorCode);
        }
    }

    /** 单次成功同步的不可变控制视图；commands 与 messages 只在主线程消费。 */
    static final class SyncResponse {
        final String serviceInstanceId;
        final String requestId;
        final ControlView control;
        final List<Command> commands;
        final List<Message> messages;
        final StopState stop;

        SyncResponse(String serviceInstanceId, String requestId, ControlView control,
                List<Command> commands, List<Message> messages, StopState stop) {
            this.serviceInstanceId = serviceInstanceId;
            this.requestId = requestId;
            this.control = control;
            this.commands = commands;
            this.messages = messages;
            this.stop = stop;
        }
    }

    /** 服务端保存的 Agent 消息投影；正文只进入 App 私有存储与界面。 */
    static final class Message {
        final String id;
        final long seq;
        final String text;
        final String createdAt;

        Message(String id, long seq, String text, String createdAt) {
            this.id = id;
            this.seq = seq;
            this.text = text;
            this.createdAt = createdAt;
        }
    }

    static final class ControlView {
        final long revision;
        final Availability availability;
        final Session session;
        final Work work;
        final Cleanup cleanup;

        ControlView(long revision, Availability availability, Session session, Work work,
                Cleanup cleanup) {
            this.revision = revision;
            this.availability = availability;
            this.session = session;
            this.work = work;
            this.cleanup = cleanup;
        }

        boolean sameAs(ControlView other) {
            return other != null && revision == other.revision
                    && availability.equals(other.availability)
                    && java.util.Objects.equals(session == null ? null : session.identity(),
                            other.session == null ? null : other.session.identity())
                    && java.util.Objects.equals(work == null ? null : work.identity(),
                            other.work == null ? null : other.work.identity())
                    && java.util.Objects.equals(cleanup == null ? null : cleanup.identity(),
                            other.cleanup == null ? null : other.cleanup.identity());
        }
    }

    static final class Availability {
        final String state;
        final String reason;

        Availability(String state, String reason) {
            this.state = state;
            this.reason = reason;
        }

        @Override public boolean equals(Object other) {
            if (!(other instanceof Availability)) {
                return false;
            }
            Availability that = (Availability) other;
            return state.equals(that.state) && java.util.Objects.equals(reason, that.reason);
        }

        @Override public int hashCode() {
            return state.hashCode();
        }
    }

    /** 控制视图会话投影；只保留界面文案需要的时段与原因，不携带任一 O/P 凭据之外的执行材料。 */
    static final class Session {
        final String origin;
        final String agent;
        final String mode;
        final String state;
        final String startedAt;
        final String expiresAt;
        final String endedAt;
        final String reason;

        Session(String origin, String agent, String mode, String state, String startedAt,
                String expiresAt, String endedAt, String reason) {
            this.origin = origin;
            this.agent = agent;
            this.mode = mode;
            this.state = state;
            this.startedAt = startedAt;
            this.expiresAt = expiresAt;
            this.endedAt = endedAt;
            this.reason = reason;
        }

        String identity() {
            return origin + '|' + agent + '|' + mode + '|' + state + '|' + startedAt + '|'
                    + expiresAt + '|' + endedAt + '|' + reason;
        }
    }

    /** 当前设备工作位置；只有类别与阶段，不放节点、坐标、输入正文或命令。 */
    static final class Work {
        final String operationId;
        final String kind;
        final String phase;

        Work(String operationId, String kind, String phase) {
            this.operationId = operationId;
            this.kind = kind;
            this.phase = phase;
        }

        String identity() {
            return operationId + '|' + kind + '|' + phase;
        }
    }

    /** 最低收尾摘要；执行链、task/recents/display 分别表达，不能合并成单一“已清理”。 */
    static final class Cleanup {
        final String state;
        final String execution;
        final String tasks;
        final String recents;
        final String display;

        Cleanup(String state, String execution, String tasks, String recents, String display) {
            this.state = state;
            this.execution = execution;
            this.tasks = tasks;
            this.recents = recents;
            this.display = display;
        }

        String identity() {
            return state + '|' + execution + '|' + tasks + '|' + recents + '|' + display;
        }
    }

    /** 响应中的停止确认；只在本拍携带 stop_pending 时出现。 */
    static final class StopState {
        final long seq;
        final String state;

        StopState(long seq, String state) {
            this.seq = seq;
            this.state = state;
        }
    }

    /** 最小停止确认；accepted 只表示停止边界已绑定，不代表物理资源已释放。 */
    static final class StopAck {
        final String serviceInstanceId;
        final boolean accepted;
        final long stopSeq;
        final String state;

        StopAck(String serviceInstanceId, boolean accepted, long stopSeq, String state) {
            this.serviceInstanceId = serviceInstanceId;
            this.accepted = accepted;
            this.stopSeq = stopSeq;
            this.state = state;
        }
    }

    private static final class Reply {
        final int statusCode;
        final String body;
        final ConnectionConfig config;
        final String requestId;

        Reply(int statusCode, String body, ConnectionConfig config, String requestId) {
            this.statusCode = statusCode;
            this.body = body;
            this.config = config;
            this.requestId = requestId;
        }
    }

    static final class ServiceException extends Exception {
        final String title;
        final String code;

        ServiceException(String title, String detail, String code) {
            super(detail);
            this.title = title;
            this.code = code;
        }
    }
}
