(function () {
  'use strict';

  var TOKEN_KEY = 'lst_token';
  var token = localStorage.getItem(TOKEN_KEY) || '';
  var MY_TOKEN = token;
  var ME = null;
  var CAPS = {};
  var META = {};

  function $(id) { return document.getElementById(id); }

  var messagesEl = $('messages');
  var messagesInner = $('messagesInner');
  var emptyTip = $('emptyTip');
  var wsDot = $('wsDot');
  var wsText = $('wsText');
  var toastEl = $('toast');

  var settingsBtn = $('settingsBtn');
  var settingsPanel = $('settingsPanel');
  var settingsMask = $('settingsMask');
  var settingsCloseBtn = $('settingsCloseBtn');
  var usernameInput = $('usernameInput');
  var tokenText = $('tokenText');
  var logoutBtn = $('logoutBtn');

  var form = $('form');
  var contentEl = $('content');
  var sendBtn = $('sendBtn');
  var attachBtn = $('attachBtn');
  var fileInput = $('fileInput');

  var toolbar = $('toolbar');
  var selCount = $('selCount');
  var dlSelectedBtn = $('dlSelectedBtn');
  var clearSelBtn = $('clearSelBtn');

  var previewMask = $('previewMask');
  var previewBody = $('previewBody');
  var previewCloseBtn = $('previewCloseBtn');

  var qrMask = $('qrMask');
  var qrCloseBtn = $('qrCloseBtn');
  var qrCanvasWrap = $('qrCanvasWrap');
  var qrUrlEl = $('qrUrl');
  var qrTitle = $('qrTitle');

  var rowMap = new Map();
  var order = [];
  var selected = new Set();
  var ws = null, retry = 0;
  var settingsOpen = false;
  var bound = false;

  // ============================================================
  // 小工具
  // ============================================================
  function el(tag, cls, text) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text !== undefined && text !== null) e.textContent = text;
    return e;
  }

  function toast(msg) {
    if (!toastEl) return;
    toastEl.textContent = msg;
    toastEl.classList.add('show');
    clearTimeout(toast._t);
    toast._t = setTimeout(function () { toastEl.classList.remove('show'); }, 2200);
  }

  function roleName(id) {
    var roles = META.roles || [];
    for (var i = 0; i < roles.length; i++) {
      if (roles[i].id === id) return roles[i].name;
    }
    return ME && ME.role_name ? ME.role_name : ('role-' + id);
  }

  function copyText(text) {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(function () { toast('已复制'); }, function () { toast('复制失败，请手动选择'); });
      return;
    }
    toast('请手动选择复制');
  }

  function doLogout() {
    token = '';
    MY_TOKEN = '';
    localStorage.removeItem(TOKEN_KEY);
    try { fetch('/auth/logout', { method: 'POST' }); } catch (_) {}
    if (location.pathname !== '/login') location.replace('/login');
  }

  // ============================================================
  // HTTP
  // ============================================================
  function requestJSON(path, opts, useAuth) {
    opts = opts || {};
    var headers = {};
    if (opts.headers) {
      for (var k in opts.headers) {
        if (Object.prototype.hasOwnProperty.call(opts.headers, k)) headers[k] = opts.headers[k];
      }
    }
    if (useAuth && token) headers['Authorization'] = 'Bearer ' + token;
    opts.headers = headers;
    opts.cache = 'no-store';
    return fetch(path, opts).then(function (res) {
      return res.json().catch(function () { return {}; }).then(function (data) {
        if (!res.ok) {
          var err = new Error((data && data.message) || ('HTTP ' + res.status));
          err.status = res.status;
          throw err;
        }
        return data;
      });
    });
  }

  function api(path, opts) {
    return requestJSON(path, opts, true).catch(function (e) {
      if (e.status === 401) doLogout();
      throw e;
    });
  }

  // ============================================================
  // 身份 / 启动
  // ============================================================
  function applyMe(data) {
    if (!data) return;
    if (data.user) ME = data.user;
    CAPS = data.caps || {};
    META = data.meta || META;
    if (data.token) {
      token = data.token;
      MY_TOKEN = data.token;
      localStorage.setItem(TOKEN_KEY, token);
    }
    if (usernameInput && ME) usernameInput.value = ME.name || '';
    if (tokenText) tokenText.textContent = MY_TOKEN || '(token 不可用)';
  }

  function markReady() {
    window.APP_READY = true;
    try { document.dispatchEvent(new Event('app:ready')); } catch (_) {}
  }

  function boot() {
    // 先用 Cookie 取身份；失败再回退到 localStorage token。
    // 这样打开新登录链接时，不会被浏览器里残留的旧 token 干扰。
    requestJSON('/auth/me', {}, false).catch(function (e) {
      if (e.status === 401 && token) return requestJSON('/auth/me', {}, true);
      throw e;
    }).then(function (data) {
      applyMe(data);
      bindUI();
      markReady();
      loadHistory();
      connect();
    }).catch(function (e) {
      if (e.status === 401) { doLogout(); return; }
      toast(e.message || '加载失败');
    });
  }

  // ============================================================
  // UI 绑定（DOM 由服务端按角色输出，缺失的节点直接跳过）
  // ============================================================
  function bindUI() {
    if (bound) return;
    bound = true;

    if (settingsBtn) {
      settingsBtn.addEventListener('click', function () {
        settingsOpen ? closeSettings() : openSettings();
      });
    }
    if (settingsCloseBtn) settingsCloseBtn.addEventListener('click', closeSettings);
    if (settingsMask) settingsMask.addEventListener('click', closeSettings);
    document.addEventListener('keydown', function (e) { if (e.key === 'Escape' && settingsOpen) closeSettings(); });

    if (usernameInput) {
      usernameInput.addEventListener('change', function () {
        var v = usernameInput.value.trim();
        if (!v) { usernameInput.value = (ME && ME.name) || ''; return; }
        api('/profile', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ name: v })
        }).then(function (data) {
          applyMe(data);
          toast('昵称已更新');
          loadHistory();
        }).catch(function (e) { toast(e.message); });
      });
    }
    if (logoutBtn) logoutBtn.addEventListener('click', doLogout);

    bindComposer();
    bindToolbar();

    if (previewCloseBtn) previewCloseBtn.addEventListener('click', closePreview);
    if (previewMask) {
      previewMask.addEventListener('click', function (e) {
        if (e.target === previewMask) closePreview();
      });
    }
    if (qrCloseBtn) qrCloseBtn.addEventListener('click', closeQR);
    if (qrMask) {
      qrMask.addEventListener('click', function (e) {
        if (e.target === qrMask) closeQR();
      });
    }
    document.addEventListener('keydown', function (e) {
      if (e.key === 'Escape') { closePreview(); closeQR(); }
    });

    document.addEventListener('visibilitychange', function () {
      if (!document.hidden) {
        if (ws && ws.readyState === WebSocket.CLOSED) { retry = 0; connect(); }
        if (isNearBottom()) scrollToBottom();
      }
    });
  }

  function openSettings() {
    if (settingsOpen || !settingsPanel) return;
    settingsOpen = true;
    settingsPanel.classList.add('show');
    settingsMask.classList.add('show');
    settingsPanel.setAttribute('aria-hidden', 'false');
    settingsBtn.setAttribute('aria-expanded', 'true');
    // 通知角色增量脚本（如有）刷新数据。
    try { document.dispatchEvent(new Event('settings:open')); } catch (_) {}
  }

  function closeSettings() {
    if (!settingsOpen) return;
    settingsOpen = false;
    settingsPanel.classList.remove('show');
    settingsMask.classList.remove('show');
    settingsPanel.setAttribute('aria-hidden', 'true');
    settingsBtn.setAttribute('aria-expanded', 'false');
  }

  // ============================================================
  // 输入区
  // ============================================================
  function updateComposerState() {
    if (!contentEl) return;
    var hasText = contentEl.value.trim().length > 0;
    if (attachBtn) attachBtn.style.visibility = hasText ? 'hidden' : 'visible';
    if (sendBtn) sendBtn.style.display = hasText ? 'inline-flex' : 'none';
  }

  function bindComposer() {
    if (contentEl) {
      contentEl.addEventListener('input', function () {
        contentEl.style.height = 'auto';
        contentEl.style.height = Math.min(contentEl.scrollHeight, 120) + 'px';
        updateComposerState();
      });
      contentEl.addEventListener('keydown', function (e) {
        if (e.key === 'Enter' && !e.shiftKey && !e.isComposing) {
          e.preventDefault();
          submitText();
        }
      });
      updateComposerState();
    }
    if (form) {
      form.addEventListener('submit', function (e) {
        e.preventDefault();
        submitText();
      });
    }
    if (attachBtn && fileInput) {
      attachBtn.addEventListener('click', function () { fileInput.click(); });
      fileInput.addEventListener('change', function () {
        if (!fileInput.files || !fileInput.files.length) return;
        uploadFiles(Array.prototype.slice.call(fileInput.files));
        fileInput.value = '';
      });
    }
  }

  function submitText() {
    if (!contentEl) return;
    var content = contentEl.value.trim();
    if (!content) return;

    var body = new URLSearchParams();
    body.append('content', content);

    if (sendBtn) sendBtn.disabled = true;
    api('/texts', {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: body.toString()
    }).then(function (row) {
      upsertRows([row]);
      scrollToBottom();
      contentEl.value = '';
      contentEl.style.height = 'auto';
      updateComposerState();
      contentEl.focus();
    }).catch(function (err) {
      toast('发送失败：' + err.message);
    }).finally(function () {
      if (sendBtn) sendBtn.disabled = false;
    });
  }

  function uploadFiles(files) {
    var pending = files.slice();
    var total = pending.length;
    var done = 0;
    toast('正在上传 ' + total + ' 个文件…');

    function next() {
      if (!pending.length) {
        if (done > 0) toast('已上传 ' + done + ' 个文件');
        return;
      }
      var f = pending.shift();
      var fd = new FormData();
      fd.append('file', f);

      var xhr = new XMLHttpRequest();
      xhr.open('POST', '/upload');
      if (token) xhr.setRequestHeader('Authorization', 'Bearer ' + token);
      xhr.onload = function () {
        if (xhr.status === 401) { doLogout(); return; }
        if (xhr.status >= 200 && xhr.status < 300) done++;
        else toast('上传失败：' + f.name);
        next();
      };
      xhr.onerror = function () { toast('上传失败：' + f.name); next(); };
      xhr.send(fd);
    }
    next();
  }

  // ============================================================
  // 工具栏 / 勾选
  // ============================================================
  function bindToolbar() {
    if (clearSelBtn) {
      clearSelBtn.addEventListener('click', function () {
        selected.clear();
        queryAll('.file-check').forEach(function (c) { c.checked = false; });
        updateSelectionUI();
      });
    }
    if (dlSelectedBtn) dlSelectedBtn.addEventListener('click', downloadSelected);
  }

  function queryAll(sel) {
    return Array.prototype.slice.call(messagesInner.querySelectorAll(sel));
  }

  function updateSelectionUI() {
    if (!toolbar || !selCount) return;
    var n = selected.size;
    selCount.textContent = '已选 ' + n + ' 个';
    toolbar.style.display = n > 0 ? 'flex' : 'none';
  }

  function onFileCheck(e) {
    var id = e.target.value;
    if (e.target.checked) selected.add(id); else selected.delete(id);
    updateSelectionUI();
  }

  function downloadSelected() {
    var ids = Array.from(selected);
    if (!ids.length) { toast('请先勾选文件'); return; }
    var fileIds = [];
    ids.forEach(function (id) {
      var row = rowMap.get(Number(id)) || rowMap.get(id);
      if (!row) return;
      if (row.files && row.files.length) {
        row.files.forEach(function (f) { if (f.download_url) fileIds.push(f.id); });
      } else if (row.download_url) {
        fileIds.push(row.id);
      }
    });
    if (!fileIds.length) { toast('所选记录不可下载'); return; }
    fileIds.forEach(function (fid, i) {
      setTimeout(function () {
        var a = document.createElement('a');
        a.href = '/download?msg=' + encodeURIComponent(fid);
        a.download = '';
        document.body.appendChild(a);
        a.click();
        document.body.removeChild(a);
      }, i * 300);
    });
    setTimeout(function () {
      selected.clear();
      queryAll('.file-check').forEach(function (c) { c.checked = false; });
      updateSelectionUI();
    }, fileIds.length * 300 + 200);
  }

  // ============================================================
  // 消息渲染
  // ============================================================
  function getRows() {
    var out = [];
    for (var i = 0; i < order.length; i++) {
      var r = rowMap.get(order[i]);
      if (r) out.push(r);
    }
    return out;
  }

  var FIVE_MIN = 5 * 60 * 1000;

  function upsertRows(list) {
    var changed = false;
    list.forEach(function (r) {
      if (!r || typeof r.id === 'undefined') return;
      if (!rowMap.has(r.id)) order.push(r.id);
      rowMap.set(r.id, r);
      changed = true;
    });
    if (!changed) return;

    var prev = null;
    order.forEach(function (id) {
      var r = rowMap.get(id);
      if (!r) return;
      if (!prev) { r.show_divider = true; }
      else if (r.show_divider && r.ts - prev.ts <= FIVE_MIN) { r.show_divider = false; }
      prev = r;
    });
    renderAll();
  }

  function buildMetaEl(row) {
    var meta = el('div', 'meta');
    meta.appendChild(el('span', 'name', row.display_name || '匿名'));
    return meta;
  }

  function buildTextNode(row) {
    var self = !!row.is_self;
    var rowEl = el('div', 'msg-row ' + (self ? 'self' : 'other'));
    rowEl.dataset.id = row.id;
    var av = el('div', 'avatar', row.avatar_text || '?');
    var wrap = el('div', 'bubble-wrap');
    wrap.appendChild(buildMetaEl(row));
    wrap.appendChild(el('div', 'bubble', row.content));
    if (self) { rowEl.appendChild(wrap); rowEl.appendChild(av); }
    else { rowEl.appendChild(av); rowEl.appendChild(wrap); }
    return rowEl;
  }

  function fileIcon(kind) {
    if (kind === 'image') return '🖼️';
    if (kind === 'video') return '🎬';
    if (kind === 'pdf') return '📕';
    return '📄';
  }

  function buildFileItemEl(item) {
    var isPreview = !!(item.preview_url && item.preview_kind);
    var e = el('div', 'file-item' + (isPreview ? ' previewable' : ''));
    var icon = el('div', 'file-icon-sm');
    if (item.preview_kind === 'image' && item.preview_url) {
      var img = document.createElement('img');
      img.src = item.preview_url;
      img.alt = item.filename || '';
      img.loading = 'lazy';
      img.addEventListener('error', function () {
        icon.classList.remove('has-thumb');
        icon.textContent = fileIcon(item.preview_kind);
      });
      icon.appendChild(img);
      icon.classList.add('has-thumb');
    } else {
      icon.textContent = fileIcon(item.preview_kind);
    }
    var info = el('div', 'file-info');
    var fn = el('div', 'file-name', item.filename);
    fn.title = item.filename;
    info.appendChild(fn);
    info.appendChild(el('div', 'file-size', item.size_text || ''));
    e.appendChild(icon);
    e.appendChild(info);

    if (item.download_url) {
      var dl = document.createElement('a');
      dl.className = 'file-dl';
      dl.href = item.download_url;
      dl.title = '下载文件';
      dl.textContent = '⬇';
      dl.addEventListener('click', function (ev) { ev.stopPropagation(); });
      e.appendChild(dl);
    }
    if (isPreview) {
      e.addEventListener('click', function (ev) {
        if (ev.target && ev.target.classList && ev.target.classList.contains('file-dl')) return;
        openPreview(item);
      });
    }
    return e;
  }

  function buildFileNode(row) {
    var self = !!row.is_self;
    var rowEl = el('div', 'msg-row ' + (self ? 'self' : 'other'));
    rowEl.dataset.id = row.id;
    var av = el('div', 'avatar', row.avatar_text || '?');
    var wrap = el('div', 'bubble-wrap');
    wrap.appendChild(buildMetaEl(row));

    var files = (row.files && row.files.length) ? row.files : [{
      id: row.id, filename: row.filename, filesize: row.filesize,
      size_text: row.size_text, preview_kind: row.preview_kind,
      preview_url: row.preview_url, download_url: row.download_url
    }];
    var isGroup = files.length > 1;
    var canDownload = !!row.can_download && !!CAPS.download;
    var fileRow = el('div', 'file-row');

    var chk = null;
    if (canDownload) {
      chk = document.createElement('input');
      chk.type = 'checkbox';
      chk.className = 'file-check';
      chk.value = String(row.id);
      chk.checked = selected.has(String(row.id));
      chk.addEventListener('change', onFileCheck);
    }

    var card = el('div', 'file-card' + (isGroup ? ' grouped' : '') + (!isGroup && files[0].preview_kind ? ' previewable' : ''));
    if (isGroup) card.title = '点击展开/收起';
    else if (files[0].preview_kind) card.title = '点击预览';

    var icon = el('div', 'file-icon');
    if (isGroup) {
      icon.textContent = '📁';
    } else if (files[0].preview_kind === 'image' && files[0].preview_url) {
      var thumb = document.createElement('img');
      thumb.className = 'file-thumb';
      thumb.src = files[0].preview_url;
      thumb.alt = files[0].filename || '';
      thumb.loading = 'lazy';
      thumb.addEventListener('error', function () {
        icon.classList.remove('has-thumb');
        icon.textContent = fileIcon(files[0].preview_kind);
      });
      icon.appendChild(thumb);
      icon.classList.add('has-thumb');
    } else {
      icon.textContent = fileIcon(files[0].preview_kind);
    }

    var info = el('div', 'file-info');
    var fn = el('div', 'file-name', isGroup ? (files.length + ' 个文件') : files[0].filename);
    fn.title = isGroup ? (files.length + ' 个文件') : files[0].filename;
    info.appendChild(fn);
    info.appendChild(el('div', 'file-size', isGroup ? '点击展开查看' : (files[0].size_text || '')));
    card.appendChild(icon);
    card.appendChild(info);

    if (canDownload) {
      var dl = document.createElement('a');
      dl.className = 'file-dl';
      dl.href = isGroup ? 'javascript:void(0)' : (files[0].download_url || '#');
      dl.title = isGroup ? '下载全部' : '下载文件';
      dl.textContent = '⬇';
      dl.addEventListener('click', function (ev) {
        ev.stopPropagation();
        if (!isGroup) return;
        files.forEach(function (it, i) {
          setTimeout(function () {
            var a = document.createElement('a');
            a.href = it.download_url;
            a.download = '';
            document.body.appendChild(a);
            a.click();
            document.body.removeChild(a);
          }, i * 300);
        });
      });
      card.appendChild(dl);
    }

    var expandWrap = null;
    if (isGroup) {
      expandWrap = el('div', 'file-expand');
      files.forEach(function (it) { expandWrap.appendChild(buildFileItemEl(it)); });
      var collapseBtn = el('button', 'file-collapse-btn', '收起 ▲');
      collapseBtn.type = 'button';
      collapseBtn.addEventListener('click', function (ev) {
        ev.stopPropagation();
        collapseGroup();
      });
      expandWrap.appendChild(collapseBtn);
    }
    function expandGroup() {
      if (!isGroup || !expandWrap) return;
      card.classList.add('expanded');
      if (!expandWrap.parentNode) fileRow.appendChild(expandWrap);
    }
    function collapseGroup() {
      if (!isGroup) return;
      card.classList.remove('expanded');
      if (expandWrap && expandWrap.parentNode) expandWrap.parentNode.removeChild(expandWrap);
    }
    card.addEventListener('click', function () {
      if (isGroup) {
        if (card.classList.contains('expanded')) collapseGroup();
        else expandGroup();
      } else if (canDownload && files[0].preview_kind) {
        openPreview(files[0]);
      }
    });

    fileRow.appendChild(card);
    wrap.appendChild(fileRow);

    if (self) {
      if (chk) rowEl.appendChild(chk);
      rowEl.appendChild(wrap);
      rowEl.appendChild(av);
    } else {
      if (chk) rowEl.appendChild(chk);
      rowEl.appendChild(av);
      rowEl.appendChild(wrap);
    }
    return rowEl;
  }

  function buildMsgNode(row) {
    return row.kind === 'file' ? buildFileNode(row) : buildTextNode(row);
  }

  function renderAll() {
    var rows = getRows();
    if (!rows.length) {
      messagesInner.innerHTML = '';
      messagesInner.appendChild(emptyTip);
      emptyTip.style.display = 'block';
      updateSelectionUI();
      return;
    }
    emptyTip.style.display = 'none';
    var frag = document.createDocumentFragment();
    rows.forEach(function (r) {
      if (r.show_divider) {
        var div = el('div', 'time-divider');
        div.appendChild(el('span', '', r.time_label || ''));
        frag.appendChild(div);
      }
      frag.appendChild(buildMsgNode(r));
    });
    messagesInner.innerHTML = '';
    messagesInner.appendChild(frag);
    updateSelectionUI();
  }

  function scrollToBottom() {
    requestAnimationFrame(function () { messagesEl.scrollTop = messagesEl.scrollHeight; });
  }
  function isNearBottom() {
    return messagesEl.scrollHeight - messagesEl.scrollTop - messagesEl.clientHeight < 80;
  }

  // ============================================================
  // 预览 / 二维码
  // ============================================================
  function openPreview(item) {
    previewBody.innerHTML = '';
    var pk = item.preview_kind, u = item.preview_url;
    if (!pk || !u) return;
    if (pk === 'image') {
      var img = document.createElement('img');
      img.src = u; img.alt = item.filename || '';
      previewBody.appendChild(img);
    } else if (pk === 'video') {
      var v = document.createElement('video');
      v.src = u; v.controls = true; v.autoplay = true; v.playsInline = true;
      previewBody.appendChild(v);
    } else if (pk === 'pdf') {
      var ifr = document.createElement('iframe');
      ifr.src = u; ifr.title = item.filename || 'PDF';
      previewBody.appendChild(ifr);
    }
    previewMask.classList.add('show');
  }

  function closePreview() {
    if (!previewMask) return;
    previewMask.classList.remove('show');
    previewBody.innerHTML = '';
  }

  function openQR(title, loginURL) {
    if (!qrMask) return;
    qrTitle.textContent = title || '新用户登录二维码';
    qrCanvasWrap.innerHTML = '<div style="color:#888;font-size:13px;">加载中…</div>';
    qrUrlEl.textContent = loginURL || '';
    qrMask.classList.add('show');
    var img = document.createElement('img');
    img.alt = '新用户登录二维码';
    img.src = '/share/qr?url=' + encodeURIComponent(loginURL);
    img.onerror = function () {
      qrCanvasWrap.innerHTML = '<div style="color:#fa5151;font-size:13px;">二维码生成失败</div>';
    };
    qrCanvasWrap.innerHTML = '';
    qrCanvasWrap.appendChild(img);
  }

  function closeQR() {
    if (!qrMask) return;
    qrMask.classList.remove('show');
    qrCanvasWrap.innerHTML = '';
  }

  // ============================================================
  // 历史 / WebSocket
  // ============================================================
  function loadHistory() {
    return api('/history?limit=200').then(function (data) {
      applyMe(data);
      rowMap.clear();
      order.length = 0;
      selected.clear();
      upsertRows(data.rows || []);
      scrollToBottom();
    }).catch(function (e) {
      toast(e.message || '读取历史失败');
    });
  }

  function setStatus(on, text) {
    if (wsDot) wsDot.className = 'dot ' + (on ? 'on' : 'off');
    if (wsText) wsText.textContent = text;
  }

  function connect() {
    if (!token) return;
    var proto = location.protocol === 'https:' ? 'wss://' : 'ws://';
    try {
      ws = new WebSocket(proto + location.host + '/ws?token=' + encodeURIComponent(token));
    } catch (e) { schedule(); return; }
    setStatus(false, '连接中');
    ws.onopen = function () { retry = 0; setStatus(true, '已连接'); };
    ws.onmessage = function (e) {
      var ev;
      try { ev = JSON.parse(e.data); } catch (_) { return; }
      if (!ev || !ev.type) return;
      if (ev.type === 'history') {
        rowMap.clear(); order.length = 0; selected.clear();
        upsertRows(ev.rows || []);
        scrollToBottom();
      } else if (ev.type === 'message_upsert' && ev.row) {
        var atBottom = isNearBottom();
        upsertRows([ev.row]);
        if (atBottom) scrollToBottom();
      } else if (ev.type === 'acl_changed') {
        toast('权限已更新');
        loadHistory();
      } else if (ev.type === 'permissions_changed') {
        toast('权限已更新，正在刷新…');
        setTimeout(function () { location.reload(); }, 500);
      } else if (ev.type === 'session_revoked') {
        token = '';
        MY_TOKEN = '';
        localStorage.removeItem(TOKEN_KEY);
        toast('登录已失效，请重新登录');
        setTimeout(doLogout, 800);
      }
    };
    ws.onclose = function () { setStatus(false, '已断开'); schedule(); };
    ws.onerror = function () { try { ws.close(); } catch (_) {} };
  }

  function schedule() {
    if (!token) return;
    var d = Math.min(30000, 1000 * Math.pow(2, retry++));
    setTimeout(connect, d);
  }

  // ============================================================
  // 暴露给 admin.js / host.js 的公共接口
  // ============================================================
  window.App = {
    api: api,
    toast: toast,
    el: el,
    openQR: openQR,
    copyText: copyText,
    roleName: roleName,
    loadHistory: loadHistory,
    getState: function () {
      return { ME: ME, CAPS: CAPS, META: META, MY_TOKEN: MY_TOKEN };
    }
  };

  boot();
})();
