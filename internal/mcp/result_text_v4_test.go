package mcp

import (
	"strings"
	"testing"
)

func TestResultV4GoldenActSuccess(t *testing.T) {
	raw := []byte(`{"operation_id":"P7391_12","outcome":"executed",` +
		`"observation":{"status":"complete","id":"O4821_85","origin":"P7391_1","revision":85,` +
		`"captured_at":"2026-11-21T22:08:55.867164298Z","actionable_until":"2026-11-21T22:09:55.867164298Z",` +
		`"quality":{"state":"stable","samples":1,"stop_reason":"stable"},` +
		`"screen":{"width":1080,"height":2400,"rotation":0,"display_id":148},` +
		`"foreground":{"state":"present","package":"tv.danmaku.bili","task_id":177},` +
		`"tree":{"format":"full","scope":"reported_visible_with_context","view":"compact",` +
		`"root":{"ref":"n1","role":"root","bounds":[0,0,1080,2400]}}}}`)
	text := assertResultTextParity(t, raw, false)
	want := "result v4 ok op=P7391_12\n" +
		"obs O4821_85 fg=tv.danmaku.bili#177 at=22:08:55\n" +
		"\ntree v5 full nodes=1\n\nn1 root 0,0,1080,2400\nend tree\nend result\n"
	if text != want {
		t.Fatalf("golden mismatch\n got:\n%s\nwant:\n%s", text, want)
	}
}

func TestResultV4HeaderRules(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		isError bool
		want    string
	}{
		{
			name:    "error keeps executed outcome",
			raw:     `{"operation_id":"P1","outcome":"executed","error":{"code":"e","message":"m"}}`,
			isError: true,
			want:    "result v4 err op=P1 outcome=executed\n",
		},
		{
			name: "status summary keeps state and outcome",
			raw:  `{"operation_id":"P2","operation_state":"finished","outcome":"executed"}`,
			want: "result v4 ok op=P2 state=finished outcome=executed\n",
		},
		{
			name: "not executed keeps outcome",
			raw:  `{"operation_id":"P3","outcome":"not_executed"}`,
			want: "result v4 ok op=P3 outcome=not_executed\n",
		},
		{
			name: "uncertain keeps outcome",
			raw:  `{"operation_id":"P4","outcome":"uncertain"}`,
			want: "result v4 ok op=P4 outcome=uncertain\n",
		},
		{
			name: "partial keeps outcome",
			raw:  `{"operation_id":"P6","outcome":"partial"}`,
			want: "result v4 ok op=P6 outcome=partial\n",
		},
		{
			name: "phase retained",
			raw:  `{"operation_id":"P5","outcome":"executed","phase":"completed"}`,
			want: "result v4 ok op=P5 phase=completed\n",
		},
		{
			name: "bare ok",
			raw:  `{}`,
			want: "result v4 ok\n",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			text := assertResultTextParity(t, []byte(testCase.raw), testCase.isError)
			if !strings.HasPrefix(text, testCase.want) {
				t.Fatalf("header = %q, want prefix %q", strings.Split(text, "\n")[0], testCase.want)
			}
		})
	}
}

func TestResultV4TextInputSubmitLine(t *testing.T) {
	// The partial text act carries the renamed write/submit snapshot and its
	// durable observe(P) recovery; the exact P is this operation's own id.
	text := assertResultTextProjection(t, []byte(`{"operation_id":"P6","outcome":"partial",`+
		`"text_input":{"write":"executed","submit":"not_executed"},`+
		`"error":{"code":"text_submit_not_executed","message":"写入已确认，提交未派发"},`+
		`"recovery":{"entry":"observe","reference":"P","input_state":"retired"}}`), true, "O1")
	requireTestContains(t, text, "text_input write=executed submit=not_executed\n")
	requireTestContains(t, text, "recovery entry=observe ref=P6 input=retired\n")
	requireTestNotContains(t, text, "enter=")
}

func TestResultV4ObservationStatusOmission(t *testing.T) {
	text := assertResultTextParity(t, []byte(`{"observation":{"status":"complete","id":"O1"}}`), false)
	requireTestContains(t, text, "\nobs O1\n")
	requireTestNotContains(t, text, "status=complete")
	for _, status := range []string{"partial", "failed", "unexpected"} {
		text := assertResultTextParity(t, []byte(`{"observation":{"status":"`+status+`","id":"O1"}}`), true)
		requireTestContains(t, text, "obs O1 status="+status+"\n")
	}
	// A complete observation with no id and no other member must never become a
	// bare "obs" line.
	text = assertResultTextParity(t, []byte(`{"observation":{"status":"complete"}}`), false)
	requireTestContains(t, text, "\nobs status=complete\n")
}

func TestResultV4QualityEffectOmissionBoundaries(t *testing.T) {
	stable := assertResultTextParity(t, []byte(`{"observation":{"status":"complete","id":"O1",`+
		`"quality":{"state":"stable","samples":3,"stop_reason":"stable"}}}`), false)
	requireTestNotContains(t, stable, "quality=")
	requireTestNotContains(t, stable, "samples=")
	notSampled := assertResultTextParity(t, []byte(`{"observation":{"status":"complete","id":"O1",`+
		`"quality":{"state":"unknown","samples":0,"stop_reason":"not_sampled"}}}`), false)
	requireTestNotContains(t, notSampled, "quality=")
	unstable := assertResultTextParity(t, []byte(`{"observation":{"status":"partial","id":"O1",`+
		`"quality":{"state":"unstable","samples":4,"stop_reason":"max_samples"}}}`), true)
	requireTestNotContains(t, unstable, "quality=")
	requireTestNotContains(t, unstable, "samples=")
	unknown := assertResultTextParity(t, []byte(`{"observation":{"status":"partial","id":"O1",`+
		`"quality":{"state":"unknown","samples":1,"stop_reason":"unreadable"}}}`), true)
	requireTestNotContains(t, unknown, "quality=")

	effect := assertResultTextParity(t, []byte(`{"outcome":"executed","effect":{"state":"observed"}}`), false)
	requireTestNotContains(t, effect, "effect=")
	unknownEffect := assertResultTextParity(t, []byte(`{"outcome":"uncertain","effect":{"state":"unknown"}}`), false)
	requireTestNotContains(t, unknownEffect, "effect=")
}

func TestResultV4ScreenAndRotationRules(t *testing.T) {
	// A start first frame with a session always carries the screen, including a
	// missing display id rendered without "@".
	text := assertResultTextParity(t, []byte(`{"operation_id":"P1","outcome":"executed",`+
		`"session":{"origin":"P1","state":"active","mode":"virtual"},`+
		`"observation":{"status":"complete","id":"O1","captured_at":"2026-11-21T00:00:00Z",`+
		`"screen":{"width":1080,"height":2400,"rotation":0}}}`), false)
	requireTestContains(t, text, "screen=1080x2400\n")
	// A normal tree frame without a session omits width/height but never a
	// non-zero rotation.
	text = assertResultTextParity(t, []byte(`{"observation":{"status":"complete","id":"O1",`+
		`"screen":{"width":1080,"height":2400,"rotation":2,"display_id":12},`+
		`"tree":{"format":"full","scope":"reported_visible_with_context","view":"compact",`+
		`"root":{"ref":"n1","role":"root","bounds":[0,0,1080,2400]}}}}`), false)
	requireTestNotContains(t, text, "screen=1080x2400")
	requireTestContains(t, text, "rot=2")
	requireTestNotContains(t, text, "@12")
}

func TestResultV4TimestampsAndImages(t *testing.T) {
	text := assertResultTextParity(t, []byte(`{"operation_id":"P1",`+
		`"image":{"captured_at":"2026-11-18T09:08:07.123456789+08:00","width":461,"height":1024,`+
		`"media_type":"image/jpeg","read_only":true,`+
		`"screen":{"width":1080,"height":2400,"rotation":0,"display_id":115}}}`), false)
	requireTestContains(t, text, "image 461x1024 read_only screen=1080x2400@115 at=01:08:07\n")
	requireTestNotContains(t, text, "media=")
	png := assertResultTextParity(t, []byte(`{"image":{"width":2,"height":3,"media_type":"image/png"}}`), false)
	requireTestContains(t, png, "image 2x3 media=image/png\n")
	// An unparseable timestamp is preserved verbatim when it is a safe token
	// and otherwise falls back to JSON.
	odd := assertResultTextParity(t, []byte(`{"observation":{"status":"complete","id":"O1","captured_at":"not-a-time"}}`), false)
	requireTestContains(t, odd, "at=not-a-time")
	fallback := assertResultTextParity(t, []byte(`{"observation":{"status":"complete","id":"O1","captured_at":"not a time"}}`), false)
	requireTestContains(t, fallback, `json v1 path="/observation/captured_at"`)
}

// TestResultV4VisualHintLine pins the optional observation.visual member: it is
// a bare token after at= and before screen=, an absent member emits nothing, and
// a value that is not a safe token falls back to the json v1 row instead of
// corrupting the obs line.
func TestResultV4VisualHintLine(t *testing.T) {
	text := assertResultTextParity(t, []byte(`{"observation":{"status":"complete","id":"O1",`+
		`"captured_at":"2026-11-21T22:08:55Z","visual":"image_heavy(52%)",`+
		`"screen":{"width":1080,"height":2400,"rotation":0}}}`), false)
	requireTestContains(t, text, "obs O1 at=22:08:55 visual=image_heavy(52%) screen=1080x2400\n")
	sparse := assertResultTextParity(t, []byte(`{"observation":{"status":"complete","id":"O1",`+
		`"visual":"sparse_tree"}}`), false)
	requireTestContains(t, sparse, "obs O1 visual=sparse_tree\n")
	absent := assertResultTextParity(t, []byte(`{"observation":{"status":"complete","id":"O1"}}`), false)
	requireTestNotContains(t, absent, "visual=")
	fallback := assertResultTextParity(t, []byte(`{"observation":{"status":"complete","id":"O1",`+
		`"visual":"image heavy"}}`), false)
	requireTestContains(t, fallback, `json v1 path="/observation/visual" value="image heavy"`)
	requireTestNotContains(t, fallback, "visual=image heavy")
	nonString := assertResultTextParity(t, []byte(`{"observation":{"status":"complete","id":"O1",`+
		`"visual":null}}`), false)
	requireTestContains(t, nonString, `json v1 path="/observation/visual" value=null`)
}

func TestResultV4ObservationChangeAndHint(t *testing.T) {
	text := assertResultTextParity(t, []byte(`{"observation":{"status":"complete","id":"O1",`+
		`"hint":"snap","tree_change":"detail_only","visual":"media_surface",`+
		`"screen":{"width":1080,"height":2400,"rotation":0}}}`), false)
	requireTestContains(t, text, "obs O1 visual=media_surface change=detail_only hint=snap screen=1080x2400\n")

	for _, member := range []struct {
		key    string
		column string
		value  string
	}{
		{"tree_change", "change", "unchanged"},
		{"hint", "hint", "wait_for"},
	} {
		t.Run(member.key, func(t *testing.T) {
			prefix := `{"observation":{"status":"complete","id":"O1","` + member.key + `":`
			text := assertResultTextParity(t, []byte(prefix+`"`+member.value+`"}}`), false)
			requireTestContains(t, text, "obs O1 "+member.column+"="+member.value+"\n")
			for _, raw := range []string{`"not a token"`, `""`, `null`, `42`} {
				text := assertResultTextParity(t, []byte(prefix+raw+`}}`), false)
				requireTestContains(t, text, `json v1 path="/observation/`+member.key+`" value=`+raw+"\n")
				requireTestNotContains(t, text, member.column+"=")
			}
		})
	}
}

func TestResultV4NotifyAggregate(t *testing.T) {
	text := assertResultTextParity(t, []byte(`{"outcome":"executed","message_id":"M1","saved":true,`+
		`"notification":{"state":"queued","error":{"code":"notification_unavailable","message":"通知不可用",`+
		`"details":{"phase":"delivery"}}}}`), false)
	requireTestContains(t, text, "notify id=M1 saved=true state=queued\n")
	requireTestContains(t, text, `notify.error notification_unavailable "通知不可用"`+"\n")
	requireTestContains(t, text, `notify.error.details={"phase":"delivery"}`+"\n")
	only := assertResultTextParity(t, []byte(`{"notification":{"state":"saved"}}`), false)
	requireTestContains(t, only, "\nnotify state=saved\n")
}

func TestResultV4SessionLines(t *testing.T) {
	text := assertResultTextParity(t, []byte(`{"session":null}`), false)
	requireTestContains(t, text, "\nsession=null\n")
	text = assertResultTextParity(t, []byte(`{"session":{"origin":"P1","state":"active","mode":"virtual","agent":"local-agent",`+
		`"started_at":"2026-11-21T22:05:10.123Z","expires_at":"2026-11-21T22:10:10Z"}}`), false)
	requireTestContains(t, text, "session active mode=virtual origin=P1 started=22:05:10 expires=22:10:10\n")
	requireTestNotContains(t, text, "agent=")
}

func TestResultV4AppTriStateAndCollections(t *testing.T) {
	// Tri-state: absent members stay absent, explicit null stays null, false
	// stays false, and empty PID arrays remain distinct from absence.
	raw := []byte(`{"operation_id":"P1","outcome":"executed","app":{"kind":"kill","package":"p",` +
		`"android_user_id":0,"source_task_id":42,"target_task_ids":[42,43],` +
		`"display_id":12,"remaining_task_ids":[],"target_pids":[1234,1235],"killed_pids":[],` +
		`"remaining_pids":[],"restarted":null,"package_state":"stopped_at_check"}}`)
	text := assertResultTextParity(t, raw, false)
	requireTestContains(t, text, "app kill pkg=p user=0 src_task=42 target_tasks=[42,43] display=12 remaining_tasks=[] target_pids=[1234,1235] killed_pids=[] remaining_pids=[] restarted=null package_state=stopped_at_check\n")

	open := assertResultTextParity(t, []byte(`{"operation_id":"P2","outcome":"executed","app":{"kind":"open",`+
		`"package":"p","task_id":42,"display_id":12,"ownership":"owned","reused":false,"state_preserved":false}}`), false)
	requireTestContains(t, open, "app open pkg=p task=42 display=12 ownership=owned reused=false state_preserved=false\n")

	// Empty collections still emit count=0 blocks.
	find := assertResultTextParity(t, []byte(`{"operation_id":"P3","app":{"kind":"find","items":[],"total":0,"truncated":false,"captured_at":"2026-11-21T22:05:20Z"}}`), false)
	requireTestContains(t, find, "\napp find total=0 truncated=false at=22:05:20\n\napp_items v1 kind=find count=0\nend app_items\n")

	findNames := assertResultTextParity(t, []byte(`{"operation_id":"P4","app":{"kind":"find","items":[`+
		`{"name":"哔哩哔哩","package":"tv.danmaku.bili"},{"name":"知乎","package":"com.zhihu.android"}],`+
		`"total":2,"truncated":false}}`), false)
	requireTestContains(t, findNames, "app_items v1 kind=find count=2\ntv.danmaku.bili \"哔哩哔哩\"\ncom.zhihu.android \"知乎\"\nend app_items\n")

	list := assertResultTextParity(t, []byte(`{"operation_id":"P5","app":{"kind":"list","display_id":12,`+
		`"items":[{"task_id":42,"package":"p","ownership":"owned"},`+
		`{"task_id":43,"package":"q","ownership":"borrowed","origin_display":0}],`+
		`"total":2,"truncated":false,"foreground":{"state":"present","package":"p","task_id":42}}}`), false)
	requireTestContains(t, list, "app list display=12 total=2 truncated=false fg=p#42\n")
	requireTestContains(t, list, "app_items v1 kind=list count=2\n#42 p owned\n#43 q borrowed origin_display=0\nend app_items\n")

	closeText := assertResultTextParity(t, []byte(`{"operation_id":"P6","outcome":"uncertain","app":{"kind":"close",`+
		`"display_id":12,"tasks":[{"task_id":42,"outcome":"executed","recents_cleaned":true},`+
		`{"task_id":43,"outcome":"uncertain","recents_cleaned":null,`+
		`"error":{"code":"task_remove_unconfirmed","message":"关闭未确认","details":{"phase":"close"}}}],"complete":false}}`), true)
	requireTestContains(t, closeText, "app close display=12 complete=false\n")
	requireTestContains(t, closeText, "app_tasks v1 count=2\n#42 executed recents_cleaned=true\n#43 uncertain recents_cleaned=null error=task_remove_unconfirmed \"关闭未确认\" details={\"phase\":\"close\"}\nend app_tasks\n")
	closeEmpty := assertResultTextParity(t, []byte(`{"operation_id":"P7","outcome":"executed","app":{"kind":"close","tasks":[],"complete":true}}`), false)
	requireTestContains(t, closeEmpty, "\napp_tasks v1 count=0\nend app_tasks\n")
}

// TestResultV4AppNewMembersAndFallback pins the kill projection members added
// by contract 31 and proves an unknown app member still falls back at
// /app/<name>, while an unrecognized package_state falls back for the whole
// object instead of publishing an uncontrolled conclusion.
func TestResultV4AppNewMembersAndFallback(t *testing.T) {
	text := assertResultTextParity(t, []byte(`{"operation_id":"P1","app":{"kind":"kill","package":"p",`+
		`"android_user_id":0,"target_task_ids":[],"remaining_task_ids":[],`+
		`"package_state":"unknown","future":1}}`), false)
	requireTestContains(t, text, "app kill pkg=p user=0 target_tasks=[] remaining_tasks=[] package_state=unknown\n")
	requireTestContains(t, text, `json v1 path="/app/future" value=1`)

	invalid := assertResultTextParity(t, []byte(`{"operation_id":"P2","app":{"kind":"kill","package":"p",`+
		`"package_state":"done"}}`), false)
	requireTestContains(t, invalid, `json v1 path="/app" value={"kind":"kill","package":"p","package_state":"done"}`)
	requireTestNotContains(t, invalid, "package_state=done")
}

func TestResultV4WaitDiagnosisCleanupError(t *testing.T) {
	wait := assertResultTextParity(t, []byte(`{"wait":{"trigger":"query","matched":["query","absent_query"],"elapsed_ms":1200}}`), false)
	requireTestContains(t, wait, "wait trigger=query matched=query,absent_query elapsed=1200ms\n")

	diagnosis := assertResultTextParity(t, []byte(`{"diagnosis":{"summary":"诊断","issues":[],"timing_ms":{"capture":7},`+
		`"coverage":"request","truncated":false,"operation_id":"P1","outcome":"executed"}}`), false)
	requireTestContains(t, diagnosis, `diagnosis summary="诊断" coverage=request truncated=false op=P1 outcome=executed`+"\n")
	requireTestContains(t, diagnosis, "diagnosis.issues=[]\n")
	requireTestContains(t, diagnosis, `diagnosis.timing_ms={"capture":7}`+"\n")

	cleanup := assertResultTextParity(t, []byte(`{"cleanup":{"state":"pending","execution":"confirmed","tasks":"pending",`+
		`"error":{"code":"cleanup_unconfirmed","message":"收尾未确认"}}}`), false)
	requireTestContains(t, cleanup, "cleanup pending exec=confirmed tasks=pending\n")
	requireTestContains(t, cleanup, `cleanup.error cleanup_unconfirmed "收尾未确认"`+"\n")

	errorText := assertResultTextParity(t, []byte(`{"error":{"code":"target_changed","message":"a=b\n\"quoted\"",`+
		`"details":{"candidate_count":0}}}`), true)
	requireTestContains(t, errorText, `error target_changed "a=b\n\"quoted\""`+"\n")
	requireTestContains(t, errorText, `error.details={"candidate_count":0}`+"\n")
}

func TestResultV4UnknownAndInvalidTokensFallback(t *testing.T) {
	text := assertResultTextParity(t, []byte(`{"future/~key":{"x":[null,false,0,"",[]]},`+
		`"observation":{"status":"complete","id":"O1","future":{"nested":true}}}`), false)
	requireTestContains(t, text, `json v1 path="/future~1~0key" value={"x":[null,false,0,"",[]]}`)
	requireTestContains(t, text, `json v1 path="/observation/future" value={"nested":true}`)

	invalid := assertResultTextParity(t, []byte(`{"operation_id":"bad op","error":{"code":"bad = token","message":"m"}}`), true)
	requireTestContains(t, invalid, `json v1 path="/operation_id" value="bad op"`)
	requireTestContains(t, invalid, `json v1 path="/error"`)

	// An unexpected null for a non-nullable object falls back instead of being
	// silently folded into absence.
	nullObservation := assertResultTextParity(t, []byte(`{"observation":null}`), true)
	requireTestContains(t, nullObservation, `json v1 path="/observation" value=null`)
}

func TestResultV4NestedUnknownFieldsFallback(t *testing.T) {
	// A future tree member cannot enter the unchanged tree v5 block, so the
	// whole tree keeps its facts through json v1.
	tree := assertResultTextParity(t, []byte(`{"observation":{"status":"complete","id":"O1",`+
		`"tree":{"format":"full","scope":"reported_visible_with_context","view":"compact",`+
		`"root":{"ref":"n1","role":"root","bounds":[0,0,10,10],"future":true}}}}`), false)
	requireTestContains(t, tree, `json v1 path="/observation/tree"`)
	// Unknown app members also fall back in place.
	app := assertResultTextParity(t, []byte(`{"app":{"kind":"find","items":[],"total":0,"future":[]}}`), false)
	requireTestContains(t, app, `json v1 path="/app/future" value=[]`)
	// Unknown notification members fall back in place.
	notify := assertResultTextParity(t, []byte(`{"notification":{"state":"saved","future":1}}`), false)
	requireTestContains(t, notify, `json v1 path="/notification/future" value=1`)
	// Future app item/task members are preserved through json v1 even inside
	// the compact collection blocks.
	appItem, err := encodeResultText([]byte(`{"app":{"kind":"find","items":[{"name":"n","package":"p","future":1}],"total":1}}`), false, "")
	if err != nil {
		t.Fatal(err)
	}
	requireTestContains(t, appItem, `json v1 path="/app/items/0/future" value=1`)
	appTask, err := encodeResultText([]byte(`{"app":{"kind":"close","tasks":[{"task_id":1,"outcome":"executed","future":1}],"complete":true}}`), false, "")
	if err != nil {
		t.Fatal(err)
	}
	requireTestContains(t, appTask, `json v1 path="/app/tasks/0/future" value=1`)
}

func TestResultV4DecoderRejectsMalformed(t *testing.T) {
	cases := []string{
		"result v3 is_error=false\nend result\n",
		"result v4 ok\r\nend result\r\n",
		"result v4 ok\nend result",
		"result v4 ok\nend result\n\n",
		"result v4 maybe\nend result\n",
		"result v4 ok\nbare line\nend result\n",
		"result v4 ok\nobs\nend result\n",
		"result v4 ok\njson v1 path=\"bad~2\" value=1\nend result\n",
	}
	for _, text := range cases {
		if _, _, err := decodeResultTextV4(t, text); err == nil {
			t.Fatalf("malformed v4 text was accepted:\n%s", text)
		}
	}
}

func TestResultV4EmptyAndMinimalFrames(t *testing.T) {
	if text := assertResultTextParity(t, []byte(`{}`), false); text != "result v4 ok\nend result\n" {
		t.Fatalf("empty result = %q", text)
	}
	text := assertResultTextParity(t, []byte(`{"request_closed":true,"can_start":false}`), false)
	requireTestContains(t, text, "request_closed=true\n")
	requireTestContains(t, text, "can_start=false\n")
}

func TestResultV4DefensiveFallbackNoDropAndNoBareObs(t *testing.T) {
	// Malformed list item origin_display must fall back via json v1 rather than
	// being dropped silently.
	listText, err := encodeResultText([]byte(`{"app":{"kind":"list","items":[{"task_id":7,"package":"com.example","ownership":"owned","origin_display":"bad"}]}}`), false, "")
	if err != nil {
		t.Fatal(err)
	}
	requireTestContains(t, listText, `json v1 path="/app/items/0" value={"task_id":7,"package":"com.example","ownership":"owned","origin_display":"bad"}`)

	// Invalid synthetic observation status emits member-level fallback and never
	// outputs a bare "obs" line.
	obsText, err := encodeResultText([]byte(`{"observation":{"status":123}}`), false, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(obsText, "\nobs\n") {
		t.Fatalf("synthetic invalid observation emitted bare obs line:\n%s", obsText)
	}
	requireTestContains(t, obsText, `json v1 path="/observation/status" value=123`)
	if _, _, err := decodeResultTextV4(t, obsText); err != nil {
		t.Fatalf("synthetic fallback observation did not decode: %v\n%s", err, obsText)
	}
}
