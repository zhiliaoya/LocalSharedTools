package main

import (
	"database/sql"
	"log"
	"sync"
)

// ---------- 权限位（权限树 6 条） ----------

const (
	PermView     uint32 = 1 << 0 // 查看记录
	PermSend     uint32 = 1 << 1 // 发送文本
	PermUpload   uint32 = 1 << 2 // 上传文件
	PermDownload uint32 = 1 << 3 // 下载 / 预览文件
	PermShare    uint32 = 1 << 4 // 分享 / 生成用户 / 撤销
	PermManage   uint32 = 1 << 5 // 全局管理 / 重签
)

const PermAll = PermView | PermSend | PermUpload | PermDownload | PermShare | PermManage

// 权限名称，用于审计 / 文档输出
var permNames = []struct {
	Bit  uint32
	Name string
}{
	{PermView, "查看"},
	{PermSend, "发送文本"},
	{PermUpload, "上传文件"},
	{PermDownload, "下载文件"},
	{PermShare, "分享管理"},
	{PermManage, "全局管理"},
}

// ---------- 角色编码 ----------

const (
	RoleHost  = 0
	RoleAdmin = 1
	RoleLV1   = 2
	RoleLV2   = 3
	RoleLV3   = 4
	RoleLV4   = 5
	RoleLV5   = 6
)

// ---------- 分享类型 / 文件类型 ----------

const (
	ShareTypeSpecific = 0 // 仅分享者自己的记录
	ShareTypeAll      = 1 // 全体来源的记录
)

const (
	FileTypeText  = 0 // 文本
	FileTypeImage = 1
	FileTypeVideo = 2
	FileTypeAudio = 3
	FileTypePDF   = 4
	FileTypeOther = 9 // 其他文件（非 0 即文件）
)

// ---------- 字典种子 ----------

type RoleSeed struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	PermMask    uint32 `json:"perm_mask"`
	Description string `json:"description,omitempty"`
}

type ShareTypeSeed struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

type ShareLevelSeed struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	PermMask    uint32 `json:"perm_mask"`
	Description string `json:"description,omitempty"`
}

type FileTypeSeed struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

var roleSeeds = []RoleSeed{
	{RoleHost, "host", PermAll, "主机所有者，不受分享表限制"},
	{RoleAdmin, "admin", PermView | PermSend | PermUpload | PermDownload | PermShare, "管理员，仅管理自己创建的分享"},
	{RoleLV1, "lv1", PermView, "只读"},
	{RoleLV2, "lv2", PermView | PermSend, "可发文本，不能下载"},
	{RoleLV3, "lv3", PermView | PermSend | PermDownload, "可发文本、可下载"},
	{RoleLV4, "lv4", PermView | PermSend | PermUpload, "可发文本、可上传"},
	{RoleLV5, "lv5", PermView | PermSend | PermUpload | PermDownload, "可发、可传、可下载"},
}

var shareTypeSeeds = []ShareTypeSeed{
	{ShareTypeSpecific, "指定来源", "只能看到分享者自己的记录"},
	{ShareTypeAll, "全体来源", "可以看到所有用户的记录"},
}

var shareLevelSeeds = []ShareLevelSeed{
	{1, "只读", PermView, "仅查看"},
	{2, "可下载", PermView | PermDownload, "查看 + 下载"},
	{3, "全部数据权限", PermView | PermDownload | PermSend | PermUpload, "数据权限全开，仍受角色掩码限制"},
}

var fileTypeSeeds = []FileTypeSeed{
	{FileTypeText, "文本", "文本消息"},
	{FileTypeImage, "图片", "jpg / png / gif / webp 等"},
	{FileTypeVideo, "视频", "mp4 / webm / mov 等"},
	{FileTypeAudio, "音频", "mp3 / wav / flac 等"},
	{FileTypePDF, "PDF", "pdf 文档"},
	{FileTypeOther, "其他文件", "非上述类型的文件"},
}

// ---------- 权限字典（启动时从 DB 载入） ----------

type Perm struct {
	db *sql.DB

	mu sync.RWMutex

	roles          []RoleSeed
	roleMask       map[int]uint32
	roleName       map[int]string
	shareTypes     []ShareTypeSeed
	shareTypeName  map[int]string
	shareLevels    []ShareLevelSeed
	shareLevelMask map[int]uint32
	fileTypes      []FileTypeSeed
}

func NewPerm(db *sql.DB) (*Perm, error) {
	p := &Perm{
		db:             db,
		roleMask:       make(map[int]uint32),
		roleName:       make(map[int]string),
		shareTypeName:  make(map[int]string),
		shareLevelMask: make(map[int]uint32),
	}

	rows, err := db.Query(`SELECT id, name, perm_mask, description FROM role_types ORDER BY id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var r RoleSeed
		if err := rows.Scan(&r.ID, &r.Name, &r.PermMask, &r.Description); err != nil {
			rows.Close()
			return nil, err
		}
		p.roles = append(p.roles, r)
		p.roleMask[r.ID] = r.PermMask
		p.roleName[r.ID] = r.Name
	}
	rows.Close()

	rows, err = db.Query(`SELECT id, name, description FROM share_types ORDER BY id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var s ShareTypeSeed
		if err := rows.Scan(&s.ID, &s.Name, &s.Description); err != nil {
			rows.Close()
			return nil, err
		}
		p.shareTypes = append(p.shareTypes, s)
		p.shareTypeName[s.ID] = s.Name
	}
	rows.Close()

	rows, err = db.Query(`SELECT id, name, perm_mask, description FROM share_levels ORDER BY id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var s ShareLevelSeed
		if err := rows.Scan(&s.ID, &s.Name, &s.PermMask, &s.Description); err != nil {
			rows.Close()
			return nil, err
		}
		p.shareLevels = append(p.shareLevels, s)
		p.shareLevelMask[s.ID] = s.PermMask
	}
	rows.Close()

	rows, err = db.Query(`SELECT id, name, description FROM file_types ORDER BY id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var f FileTypeSeed
		if err := rows.Scan(&f.ID, &f.Name, &f.Description); err != nil {
			rows.Close()
			return nil, err
		}
		p.fileTypes = append(p.fileTypes, f)
	}
	rows.Close()

	return p, nil
}

func (p *Perm) Roles() []RoleSeed {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]RoleSeed, len(p.roles))
	copy(out, p.roles)
	return out
}

func (p *Perm) ShareTypes() []ShareTypeSeed {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]ShareTypeSeed, len(p.shareTypes))
	copy(out, p.shareTypes)
	return out
}

func (p *Perm) ShareLevels() []ShareLevelSeed {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]ShareLevelSeed, len(p.shareLevels))
	copy(out, p.shareLevels)
	return out
}

func (p *Perm) FileTypes() []FileTypeSeed {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]FileTypeSeed, len(p.fileTypes))
	copy(out, p.fileTypes)
	return out
}

func (p *Perm) RoleMask(roleID int) uint32 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.roleMask[roleID]
}

func (p *Perm) RoleName(roleID int) string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.roleName[roleID]
}

func (p *Perm) ValidRole(roleID int) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	_, ok := p.roleMask[roleID]
	return ok
}

func (p *Perm) ValidShareType(id int) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	_, ok := p.shareTypeName[id]
	return ok
}

func (p *Perm) ValidShareLevel(id int) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	_, ok := p.shareLevelMask[id]
	return ok
}

func (p *Perm) ShareLevelMask(id int) uint32 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.shareLevelMask[id]
}

// Meta 返回前端拼装管理面板所需的字典。
func (p *Perm) Meta() map[string]interface{} {
	return map[string]interface{}{
		"roles":        p.Roles(),
		"share_types":  p.ShareTypes(),
		"share_levels": p.ShareLevels(),
		"file_types":   p.FileTypes(),
	}
}

// ---------- 能力集合（前端拼装用） ----------

type Caps struct {
	View     bool `json:"view"`
	Send     bool `json:"send"`
	Upload   bool `json:"upload"`
	Download bool `json:"download"`
	Share    bool `json:"share"`
	Manage   bool `json:"manage"`
}

func CapsFromMask(mask uint32) Caps {
	return Caps{
		View:     mask&PermView != 0,
		Send:     mask&PermSend != 0,
		Upload:   mask&PermUpload != 0,
		Download: mask&PermDownload != 0,
		Share:    mask&PermShare != 0,
		Manage:   mask&PermManage != 0,
	}
}

// ---------- 可见性判定 ----------

// Viewer 在一次请求 / 一次广播内缓存 owner -> 有效掩码，避免逐行查库。
type Viewer struct {
	p      *Perm
	caller *Caller
	mu     sync.Mutex
	masks  map[int64]uint32
}

func (p *Perm) Viewer(c *Caller) *Viewer {
	return &Viewer{p: p, caller: c, masks: make(map[int64]uint32)}
}

func (v *Viewer) Caller() *Caller { return v.caller }

// MaskFor 返回 caller 对 ownerID 的记录拥有的有效权限位。
//
//	自有记录   -> 角色掩码
//	host       -> 角色掩码（全量）
//	admin      -> 自己 + 自己创建的活跃分享对应用户
//	其他角色   -> 目标 token 的活跃分享，按分享类型解释来源，按分享等级 & 角色掩码取交
func (v *Viewer) MaskFor(ownerID int64) uint32 {
	if v == nil || v.caller == nil {
		return 0
	}
	c := v.caller
	if ownerID == c.UserID {
		return c.PermMask
	}
	if c.RoleID == RoleHost {
		return c.PermMask
	}

	v.mu.Lock()
	if m, ok := v.masks[ownerID]; ok {
		v.mu.Unlock()
		return m
	}
	v.mu.Unlock()

	mask := v.p.computeMask(c, ownerID)

	v.mu.Lock()
	v.masks[ownerID] = mask
	v.mu.Unlock()
	return mask
}

func (v *Viewer) Can(ownerID int64, need uint32) bool {
	return v.MaskFor(ownerID)&need == need
}

func (p *Perm) computeMask(c *Caller, ownerID int64) uint32 {
	if c.RoleID == RoleAdmin {
		var one int
		err := p.db.QueryRow(
			`SELECT 1 FROM shares s
			 JOIN tokens t ON t.token = s.target_token
			 WHERE s.sharer_user_id = ? AND s.active = 1 AND t.user_id = ?
			 LIMIT 1`, c.UserID, ownerID).Scan(&one)
		if err == nil {
			return c.PermMask
		}
		if err != sql.ErrNoRows {
			log.Printf("权限查询失败(admin): %v", err)
		}
		return 0
	}

	rows, err := p.db.Query(
		`SELECT share_type_id, share_level_id, sharer_user_id
		 FROM shares WHERE target_token = ? AND active = 1`, c.Token)
	if err != nil {
		log.Printf("权限查询失败(user): %v", err)
		return 0
	}
	defer rows.Close()

	var mask uint32
	for rows.Next() {
		var shareType, shareLevel int
		var sharerID int64
		if err := rows.Scan(&shareType, &shareLevel, &sharerID); err != nil {
			log.Printf("权限查询扫描失败: %v", err)
			return mask
		}
		if shareType == ShareTypeSpecific && sharerID != ownerID {
			continue
		}
		mask |= p.ShareLevelMask(shareLevel) & c.PermMask
	}
	return mask
}

// Has 判断角色掩码是否含某个能力（用于"自己的操作"类接口）。
func (c *Caller) Has(need uint32) bool {
	return c != nil && c.PermMask&need == need
}
