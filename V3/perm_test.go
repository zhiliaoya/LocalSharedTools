package main

import "testing"

func TestRoleSeedsAndCaps(t *testing.T) {
	app := newTestApp(t)
	cases := []struct {
		role int
		mask uint32
	}{
		{RoleHost, PermAll},
		{RoleAdmin, PermView | PermSend | PermUpload | PermDownload | PermShare},
		{RoleLV1, PermView},
		{RoleLV2, PermView | PermSend},
		{RoleLV3, PermView | PermSend | PermDownload},
		{RoleLV4, PermView | PermSend | PermUpload},
		{RoleLV5, PermView | PermSend | PermUpload | PermDownload},
	}
	for _, c := range cases {
		if got := app.perm.RoleMask(c.role); got != c.mask {
			t.Errorf("role %d mask = %d, want %d", c.role, got, c.mask)
		}
	}
	caps := CapsFromMask(PermView | PermDownload)
	if !caps.View || !caps.Download || caps.Send || caps.Upload || caps.Share || caps.Manage {
		t.Fatalf("caps mismatch: %+v", caps)
	}
}

func TestViewerMaskByRoleAndShare(t *testing.T) {
	app := newTestApp(t)
	host := hostCaller(t, app)
	hostRec := insertTestRecord(t, app, host, FileTypeText, "host text", 0, "")

	type userCase struct {
		name  string
		role  int
		want  uint32
		level int
	}
	cases := []userCase{
		{"u1", RoleLV1, PermView, 3},
		{"u2", RoleLV2, PermView | PermSend, 3},
		{"u3", RoleLV3, PermView | PermSend | PermDownload, 3},
		{"u4", RoleLV4, PermView | PermSend | PermUpload, 3},
		{"u5", RoleLV5, PermView | PermSend | PermUpload | PermDownload, 3},
	}
	for _, c := range cases {
		_, token := makeUser(t, app, c.name, c.role, host.UserID, ShareTypeSpecific, c.level)
		caller := callerFor(t, app, token)
		viewer := app.perm.Viewer(caller)
		if got := viewer.MaskFor(host.UserID); got != c.want {
			t.Errorf("%s mask for host = %d, want %d", c.name, got, c.want)
		}
		if got := viewer.MaskFor(hostRec.UserID); got != c.want {
			t.Errorf("%s mask for host record = %d, want %d", c.name, got, c.want)
		}
	}

	// 自有时始终是角色掩码。
	_, lv2Token := makeUser(t, app, "self", RoleLV2, host.UserID, ShareTypeSpecific, 3)
	lv2 := callerFor(t, app, lv2Token)
	if got := app.perm.Viewer(lv2).MaskFor(lv2.UserID); got != (PermView | PermSend) {
		t.Errorf("self mask = %d", got)
	}
}

func TestViewerShareTypeSpecificVsAll(t *testing.T) {
	app := newTestApp(t)
	host := hostCaller(t, app)
	_, otherToken := makeUser(t, app, "other", RoleLV5, host.UserID, ShareTypeSpecific, 3)
	other := callerFor(t, app, otherToken)
	otherRec := insertTestRecord(t, app, other, FileTypeText, "other text", 0, "")

	// 指定来源：看不到非分享者的记录。
	_, specificToken := makeUser(t, app, "specific", RoleLV5, host.UserID, ShareTypeSpecific, 3)
	specific := callerFor(t, app, specificToken)
	if got := app.perm.Viewer(specific).MaskFor(other.UserID); got != 0 {
		t.Errorf("specific source mask = %d, want 0", got)
	}

	// 全体来源：能看到任何来源的记录。
	_, allToken := makeUser(t, app, "all", RoleLV5, host.UserID, ShareTypeAll, 3)
	all := callerFor(t, app, allToken)
	if got := app.perm.Viewer(all).MaskFor(other.UserID); got != (PermView | PermSend | PermUpload | PermDownload) {
		t.Errorf("all source mask = %d", got)
	}
	if got := app.perm.Viewer(all).MaskFor(otherRec.UserID); got != 15 {
		t.Errorf("all source mask for record = %d, want 15", got)
	}
}

func TestViewerRevoke(t *testing.T) {
	app := newTestApp(t)
	host := hostCaller(t, app)
	_, token := makeUser(t, app, "revokee", RoleLV5, host.UserID, ShareTypeSpecific, 3)
	caller := callerFor(t, app, token)
	if got := app.perm.Viewer(caller).MaskFor(host.UserID); got != 15 {
		t.Fatalf("pre-revoke mask = %d", got)
	}
	if _, err := app.db.Exec(`UPDATE shares SET active=0 WHERE target_token=?`, token); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if got := app.perm.Viewer(caller).MaskFor(host.UserID); got != 0 {
		t.Fatalf("post-revoke mask = %d, want 0", got)
	}
}

func TestAdminScopeAndHostBypass(t *testing.T) {
	app := newTestApp(t)
	host := hostCaller(t, app)

	adminID, adminToken := makeUser(t, app, "admin", RoleAdmin, host.UserID, ShareTypeSpecific, 3)
	admin := callerFor(t, app, adminToken)

	// admin 不消费别人发给它的分享：看不到 host 记录。
	if got := app.perm.Viewer(admin).MaskFor(host.UserID); got != 0 {
		t.Errorf("admin mask for host = %d, want 0", got)
	}
	// admin 自己的记录按角色掩码。
	if got := app.perm.Viewer(admin).MaskFor(adminID); got != (PermView | PermSend | PermUpload | PermDownload | PermShare) {
		t.Errorf("admin self mask = %d", got)
	}

	// admin 创建的用户的记录，admin 可见。
	onboardedID, _ := makeUser(t, app, "onboarded", RoleLV1, adminID, ShareTypeSpecific, 3)
	onboardedViewer := app.perm.Viewer(admin)
	if got := onboardedViewer.MaskFor(onboardedID); got != (PermView | PermSend | PermUpload | PermDownload | PermShare) {
		t.Errorf("admin mask for onboarded = %d", got)
	}

	// host 不受分享表限制。
	hostViewer := app.perm.Viewer(host)
	if got := hostViewer.MaskFor(onboardedID); got != PermAll {
		t.Errorf("host mask = %d, want %d", got, PermAll)
	}
	if !hostViewer.Can(onboardedID, PermManage) {
		t.Error("host should have manage")
	}
}
