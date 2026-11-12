(function () {
  'use strict';

  function $(id) { return document.getElementById(id); }
  if (!window.App) return;

  // 生成结果自动清除定时器（模块级，保证多次生成只保留最后一个）
  var createResultTimer = null;

  function init() {
    var btn = $('createUserBtn');
    if (!btn) return; // 不是 admin / host 页面

    var state = window.App.getState();
    var META = state.META || {};
    var ME = state.ME || { role_id: 6 };

    var roleSel = $('newRole');
    var typeSel = $('newShareType');
    var levelSel = $('newShareLevel');

    var roles = (META.roles || []).filter(function (r) {
      if (r.id === 0) return false;
      if (ME.role_id !== 0 && r.id === 1) return false; // admin 不能建 admin
      return true;
    });
    fillSelect(roleSel, roles, function (r) { return r.name; });
    if (roleSel) {
      var lv1 = roles.filter(function (r) { return r.id === 2; })[0];
      roleSel.value = String(lv1 ? lv1.id : (roles[0] ? roles[0].id : ''));
    }
    fillSelect(typeSel, META.share_types || [], function (t) { return t.name; });
    fillSelect(levelSel, META.share_levels || [], function (l) { return l.name; });
    if (levelSel) {
      var full = (META.share_levels || []).filter(function (l) { return l.id === 3; })[0];
      if (full) levelSel.value = String(full.id);
    }

    btn.addEventListener('click', createUser);
    document.addEventListener('settings:open', loadShares);

    loadShares();
  }

  function fillSelect(sel, items, labelFn) {
    if (!sel) return;
    sel.innerHTML = '';
    items.forEach(function (it) {
      var o = document.createElement('option');
      o.value = String(it.id);
      o.textContent = labelFn(it);
      sel.appendChild(o);
    });
  }

  function createUser() {
    var nameInput = $('newUserName');
    var roleSel = $('newRole');
    var typeSel = $('newShareType');
    var levelSel = $('newShareLevel');
    var btn = $('createUserBtn');
    var result = $('createResult');

    var name = nameInput ? nameInput.value.trim() : '';
    if (!name) { window.App.toast('请填写用户名称'); return; }
    if (!roleSel || !roleSel.value) { window.App.toast('请选择角色'); return; }

    btn.disabled = true;
    window.App.api('/admin/users', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        name: name,
        role_id: Number(roleSel.value),
        share_type_id: typeSel ? Number(typeSel.value) : 0,
        share_level_id: levelSel ? Number(levelSel.value) : 3
      })
    }).then(function (data) {
      if (nameInput) nameInput.value = '';
      showCreated(result, data);
      loadShares();
      window.App.toast('已生成用户');
    }).catch(function (e) {
      window.App.toast(e.message);
    }).finally(function () {
      btn.disabled = false;
    });
  }

  function showCreated(container, data) {
    if (!container) return;

    // 清除上一次的自动清除定时器，避免多次生成时叠加
    if (createResultTimer) {
      clearInterval(createResultTimer);
      createResultTimer = null;
    }

    container.innerHTML = '';
    var box = window.App.el('div', 'token-box');

    // 顶部：倒计时提示条
    var countdownBar = window.App.el('div', 'settings-hint');
    countdownBar.style.cssText =
      'display:flex;align-items:center;justify-content:space-between;gap:8px;' +
      'margin-bottom:8px;padding:4px 8px;border-radius:6px;' +
      'background:rgba(7,193,96,.10);color:var(--wx-green-dark);font-size:12px;';
    var countdownText = window.App.el('span', '', '本条信息将在 120 秒后自动清除');
    var countdownClose = window.App.el('button', 'mini-btn', '立即清除');
    countdownClose.type = 'button';
    countdownClose.style.cssText = 'padding:2px 8px;font-size:11px;flex:0 0 auto;';
    countdownClose.addEventListener('click', function () {
      clearCreateResult(container);
    });
    countdownBar.appendChild(countdownText);
    countdownBar.appendChild(countdownClose);
    box.appendChild(countdownBar);

    box.appendChild(window.App.el('div', 'settings-hint',
      '用户：' + (data.user ? data.user.name : '') + '（' + (data.user ? data.user.role_name : '') + '）'));
    box.appendChild(window.App.el('div', 'token-value', data.token));
    box.appendChild(window.App.el('div', 'link-value', data.login_url));
    var copy = window.App.el('button', 'mini-btn', '复制登录链接');
    copy.type = 'button';
    copy.addEventListener('click', function () { window.App.copyText(data.login_url); });
    box.appendChild(copy);
    box.appendChild(window.App.el('div', 'settings-hint',
      '仅将此登录链接 / 二维码发给该用户；不要把自己的登录凭证发给他人。'));
    box.appendChild(window.App.el('div', 'settings-label', '新用户登录二维码（只发给该用户）'));
    var img = document.createElement('img');
    img.className = 'qr-inline';
    img.alt = '新用户登录二维码';
    img.src = data.qr_url;
    box.appendChild(img);
    container.appendChild(box);

    // 启动 2 分钟倒计时
    var remain = 120;
    createResultTimer = setInterval(function () {
      remain--;
      if (remain <= 0) {
        clearCreateResult(container);
        return;
      }
      countdownText.textContent = '本条信息将在 ' + remain + ' 秒后自动清除';
    }, 1000);
  }

  function clearCreateResult(container) {
    if (createResultTimer) {
      clearInterval(createResultTimer);
      createResultTimer = null;
    }
    if (container) container.innerHTML = '';
  }

  function loadShares() {
    var list = $('shareList');
    if (!list) return;
    window.App.api('/admin/shares').then(function (data) {
      var shares = data.shares || [];
      list.innerHTML = '';
      if (!shares.length) {
        list.appendChild(window.App.el('div', 'settings-hint', '暂无分享'));
        return;
      }
      shares.forEach(function (s) {
        var item = window.App.el('div', 'admin-item');
        var main = window.App.el('div', 'admin-main');
        main.appendChild(window.App.el('div', 'admin-name', s.name + '（' + s.role_name + '）'));
        main.appendChild(window.App.el('div', 'admin-meta',
          s.share_type + ' · ' + s.share_level + ' · ' + (s.active ? '有效' : '已撤销') + ' · ' + s.created_at));
        main.appendChild(window.App.el('div', 'admin-meta', 'token: ' + s.token_hint));
        item.appendChild(main);
        if (s.active) {
          var btn = window.App.el('button', 'mini-btn danger', '撤销');
          btn.type = 'button';
          btn.addEventListener('click', function () {
            if (!confirm('撤销后该用户将立即失去对分享者记录的访问，确定？')) return;
            window.App.api('/share/revoke', {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({ share_id: s.id })
            }).then(function () {
              window.App.toast('已撤销');
              loadShares();
            }).catch(function (e) { window.App.toast(e.message); });
          });
          item.appendChild(btn);
        }
        list.appendChild(item);
      });
    }).catch(function (e) {
      list.textContent = '加载失败：' + e.message;
    });
  }

  window.AdminUI = { init: init, loadShares: loadShares };
  if (window.APP_READY) init();
  else document.addEventListener('app:ready', init);
})();