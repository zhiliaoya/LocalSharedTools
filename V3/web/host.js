(function () {
  'use strict';

  function $(id) { return document.getElementById(id); }
  if (!window.App) return;

  var selectedUsers = new Set();
  var META = {};

  function init() {
    var list = $('userList');
    if (!list) return; // 不是 host 页面

    var state = window.App.getState();
    META = state.META || {};

    fillBatchRole();

    var selectAll = $('userSelectAll');
    if (selectAll) {
      selectAll.addEventListener('change', function () {
        var on = selectAll.checked;
        queryUserChecks().forEach(function (c) {
          c.checked = on;
          if (on) selectedUsers.add(c.value); else selectedUsers.delete(c.value);
        });
        refreshUserSelection();
      });
    }
    bind('batchRoleBtn', userBatchRole);
    bind('batchRevokeBtn', userBatchRevoke);
    bind('batchDisableBtn', function () { userBatchActive(false); });
    bind('batchEnableBtn', function () { userBatchActive(true); });
    document.addEventListener('settings:open', loadUsers);

    refreshUserSelection();
    loadUsers();
  }

  function bind(id, fn) {
    var b = $(id);
    if (b) b.addEventListener('click', fn);
  }

  function fillBatchRole() {
    var sel = $('batchRole');
    if (!sel) return;
    sel.innerHTML = '';
    var ph = document.createElement('option');
    ph.value = '';
    ph.textContent = '选择角色…';
    sel.appendChild(ph);
    (META.roles || []).filter(function (r) { return r.id !== 0; }).forEach(function (r) {
      var o = document.createElement('option');
      o.value = String(r.id);
      o.textContent = r.name;
      sel.appendChild(o);
    });
    sel.value = '';
  }

  function queryUserChecks() {
    var list = $('userList');
    if (!list) return [];
    return Array.prototype.slice.call(list.querySelectorAll('.user-check'));
  }

  function selectedUserIDs() {
    return Array.from(selectedUsers).map(function (id) { return Number(id); });
  }

  function refreshUserSelection() {
    var n = selectedUsers.size;
    var count = $('userSelCount');
    if (count) count.textContent = '已选 ' + n + ' 个';
    var checks = queryUserChecks();
    var all = $('userSelectAll');
    if (all) {
      all.checked = checks.length > 0 && checks.every(function (c) { return c.checked; });
      all.disabled = checks.length === 0;
    }
    ['batchRoleBtn', 'batchRevokeBtn', 'batchDisableBtn', 'batchEnableBtn'].forEach(function (id) {
      var b = $(id);
      if (b) b.disabled = n === 0;
    });
  }

  function afterUserBatch() {
    selectedUsers.clear();
    loadUsers();
  }

  function userBatchRole() {
    var ids = selectedUserIDs();
    if (!ids.length) return;
    var sel = $('batchRole');
    if (!sel || !sel.value) { window.App.toast('请先选择目标角色'); return; }
    var roleID = Number(sel.value);
    var role = window.App.roleName(roleID);
    if (!confirm('将选中的 ' + ids.length + ' 个用户权限改为「' + role + '」？')) return;
    window.App.api('/admin/users/role', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ user_ids: ids, role_id: roleID })
    }).then(function () {
      window.App.toast('已修改 ' + ids.length + ' 个用户的权限');
      afterUserBatch();
    }).catch(function (e) { window.App.toast(e.message); });
  }

  function userBatchRevoke() {
    var ids = selectedUserIDs();
    if (!ids.length) return;
    if (!confirm('撤销选中的 ' + ids.length + ' 个用户的 token？撤销后需 host 重签才能登录。')) return;
    window.App.api('/admin/users/revoke-token', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ user_ids: ids })
    }).then(function (res) {
      window.App.toast('已撤销 ' + (res.revoked || 0) + ' 个 token');
      afterUserBatch();
    }).catch(function (e) { window.App.toast(e.message); });
  }

  function userBatchActive(active) {
    var ids = selectedUserIDs();
    if (!ids.length) return;
    var label = active ? '恢复' : '软删除';
    var extra = active ? '恢复后需重签才能登录。' : '将同时撤销其 token。';
    if (!confirm('确定' + label + '选中的 ' + ids.length + ' 个用户？' + extra)) return;
    window.App.api('/admin/users/active', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ user_ids: ids, active: active })
    }).then(function () {
      window.App.toast('已' + label + ' ' + ids.length + ' 个用户');
      afterUserBatch();
    }).catch(function (e) { window.App.toast(e.message); });
  }

  function userAction(path, payload, confirmText, successText) {
    if (!confirm(confirmText)) return;
    window.App.api(path, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    }).then(function () {
      window.App.toast(successText);
      loadUsers();
    }).catch(function (e) { window.App.toast(e.message); });
  }

  function loadUsers() {
    var list = $('userList');
    if (!list) return;
    window.App.api('/admin/users').then(function (data) {
      var users = data.users || [];
      var valid = new Set();
      users.forEach(function (u) { valid.add(String(u.id)); });
      Array.from(selectedUsers).forEach(function (id) {
        if (!valid.has(id)) selectedUsers.delete(id);
      });

      list.innerHTML = '';
      users.forEach(function (u) {
        var item = window.App.el('div', 'admin-item');
        if (u.role_id !== 0) {
          var chk = document.createElement('input');
          chk.type = 'checkbox';
          chk.className = 'user-check';
          chk.value = String(u.id);
          chk.checked = selectedUsers.has(String(u.id));
          chk.addEventListener('change', function () {
            if (chk.checked) selectedUsers.add(chk.value);
            else selectedUsers.delete(chk.value);
            refreshUserSelection();
          });
          item.appendChild(chk);
        }

        var main = window.App.el('div', 'admin-main');
        main.appendChild(window.App.el('div', 'admin-name', u.name + '（' + u.role_name + '）'));
        main.appendChild(window.App.el('div', 'admin-meta',
          'ID ' + u.id + ' · ' + (u.active ? '启用' : '已停用') + ' · 活跃 token ' + u.active_tokens));
        item.appendChild(main);

        if (u.role_id !== 0) {
          var actions = window.App.el('div', 'admin-actions');
          if (u.active) {
            var btn = window.App.el('button', 'mini-btn', '重签');
            btn.type = 'button';
            btn.addEventListener('click', function () {
              if (!confirm('重签后旧 token 立即失效，确定？')) return;
              window.App.api('/admin/reissue', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ user_id: u.id })
              }).then(function (res) {
                window.App.openQR('用户 ' + u.name + ' 的新登录二维码', res.login_url);
                loadUsers();
              }).catch(function (e) { window.App.toast(e.message); });
            });
            actions.appendChild(btn);
          }
          if (u.active_tokens > 0) {
            var revoke = window.App.el('button', 'mini-btn danger', '撤销 token');
            revoke.type = 'button';
            revoke.addEventListener('click', function () {
              userAction('/admin/users/revoke-token', { user_ids: [u.id] },
                '撤销 ' + u.name + ' 的 token？撤销后需重签才能登录。', '已撤销 token');
            });
            actions.appendChild(revoke);
          }
          var toggle = window.App.el('button', 'mini-btn' + (u.active ? ' danger' : ''),
            u.active ? '软删除' : '恢复');
          toggle.type = 'button';
          toggle.addEventListener('click', function () {
            if (u.active) {
              userAction('/admin/users/active', { user_ids: [u.id], active: false },
                '软删除 ' + u.name + '？将同时撤销其 token。', '已软删除');
            } else {
              userAction('/admin/users/active', { user_ids: [u.id], active: true },
                '恢复 ' + u.name + '？恢复后需重签才能登录。', '已恢复');
            }
          });
          actions.appendChild(toggle);
          item.appendChild(actions);
        }
        list.appendChild(item);
      });
      refreshUserSelection();
    }).catch(function (e) {
      list.textContent = '加载失败：' + e.message;
    });
  }

  window.HostUI = { init: init, loadUsers: loadUsers };
  if (window.APP_READY) init();
  else document.addEventListener('app:ready', init);
})();
