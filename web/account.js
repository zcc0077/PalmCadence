(function () {
  'use strict';

  window.createPalmAccount = function (ui) {
    var byId = function (id) { return document.getElementById(id); };
    var storageKey = 'palmcadence.netease.session';
    var token = readToken();
    var profile;
    var opened = false;
    var pending = '';
    var qrRevision = 0;
    var qrTimer;
    var expires = 0;
    var retries = 0;
    var playlist;
    var tracks = [];
    var listRevision = 0;
    var listPage = 0;
    var activeSong = '';

    function readToken() {
      try { return window.localStorage.getItem(storageKey) || ''; }
      catch (error) { return ''; }
    }

    function storeToken(value) {
      token = value;
      try {
        if (value) { window.localStorage.setItem(storageKey, value); }
        else { window.localStorage.removeItem(storageKey); }
      } catch (error) {}
    }

    function call(action, fields, done) {
      var body = Object.keys(fields).map(function (name) {
        return encodeURIComponent(name) + '=' + encodeURIComponent(fields[name]);
      }).join('&');
      ui.request('/api/account/' + action, done, 'POST', body);
    }

    function refreshProfile() {
      ui.visible(byId('account-login'), !profile);
      ui.visible(byId('account-logout'), Boolean(profile));
      ui.visible(byId('account-library-title'), Boolean(profile));
      ui.message('account-name', profile ? profile.nickname || '网易云账号' : '网易云音乐');
      ui.message('account-open', profile ? '我的' : '登录');
      byId('account-open').setAttribute('aria-label', profile ? '打开我的歌单' : '登录网易云账号');
      ui.message('account-state', profile ? '已登录' : '用手机上的网易云音乐 App 扫码登录');
    }

    function clearQR(cancel) {
      var old = pending;
      pending = '';
      qrRevision += 1;
      clearTimeout(qrTimer);
      byId('account-qr').removeAttribute('src');
      ui.visible(byId('account-qr'), false);
      byId('account-login').disabled = false;
      ui.message('account-login', '生成登录二维码');
      if (cancel && old) { call('logout', {token: old}, function () {}); }
    }

    function expire(message) {
      clearQR(true);
      storeToken('');
      profile = null;
      ui.stopPlayback();
      resetList();
      refreshProfile();
      if (opened) {
        ui.visible(byId('account-home'), true);
        ui.message('account-title', '网易云账号');
      }
      ui.message('account-state', message || '网易云登录已失效，请重新扫码。');
    }

    function poll(revision) {
      if (revision !== qrRevision || !pending) { return; }
      if (Date.now() >= expires) {
        clearQR(true);
        ui.message('account-state', '二维码已过期，请重新生成。');
        return;
      }
      call('poll', {token: pending}, function (error, data) {
        if (revision !== qrRevision) { return; }
        if (error) {
          retries += 1;
          if (retries >= 3) {
            ui.message('account-state', error + ' 请刷新二维码后重试。');
            return;
          }
          ui.message('account-state', '连接不稳定，正在重试…');
        } else {
          retries = 0;
          if (data.state === 'signed_in') {
            storeToken(pending);
            profile = data.profile;
            clearQR(false);
            refreshProfile();
            showLibrary();
            return;
          }
          if (data.state === 'expired') {
            clearQR(false);
            ui.message('account-state', '二维码已过期，请重新生成。');
            return;
          }
          ui.message('account-state', data.state === 'confirming' ? '已扫码，请在手机上确认。' : '请用网易云音乐 App 扫码并确认。');
        }
        qrTimer = setTimeout(function () { poll(revision); }, 2200);
      });
    }

    function startLogin() {
      if (profile) { return; }
      clearQR(true);
      var revision = qrRevision;
      byId('account-login').disabled = true;
      ui.message('account-state', '正在向网易云申请登录码…');
      call('start', {}, function (error, data) {
        if (revision !== qrRevision) {
          if (data && data.token) { call('logout', {token: data.token}, function () {}); }
          return;
        }
        byId('account-login').disabled = false;
        if (error) {
          ui.message('account-state', error);
          return;
        }
        pending = data.token;
        retries = 0;
        expires = Date.now() + 180000;
        byId('account-qr').src = data.image;
        ui.visible(byId('account-qr'), true);
        ui.message('account-login', '刷新二维码');
        ui.message('account-state', '请用网易云音乐 App 扫码并确认。');
        qrTimer = setTimeout(function () { poll(revision); }, 1500);
      });
    }

    function resetList() {
      playlist = null;
      tracks = [];
      listRevision += 1;
      listPage = 0;
      byId('account-list').innerHTML = '';
      byId('account-scroll').scrollTop = 0;
      ui.message('account-list-status', '');
      ui.visible(byId('account-more'), false);
    }

    function showLibrary() {
      resetList();
      ui.message('account-title', profile ? '我的音乐' : '网易云账号');
      ui.visible(byId('account-home'), true);
      if (profile) { loadPage(); }
    }

    function showPlaylist(item) {
      resetList();
      playlist = item;
      ui.message('account-title', item.name);
      ui.visible(byId('account-home'), false);
      byId('account-back').focus();
      loadPage();
    }

    function playlistRow(item) {
      var row = document.createElement('li');
      var button = document.createElement('button');
      var name = document.createElement('span');
      var count = document.createElement('span');
      button.type = 'button';
      button.className = 'account-playlist';
      name.className = 'track-title';
      name.textContent = item.name;
      count.className = 'track-info';
      count.textContent = item.trackCount + ' 首';
      button.appendChild(name);
      button.appendChild(count);
      button.onclick = function () { showPlaylist(item); };
      row.appendChild(button);
      return row;
    }

    function loadPage() {
      if (!token) { return; }
      var revision = ++listRevision;
      var next = listPage + 1;
      var more = byId('account-more');
      var action = playlist ? 'tracks' : 'playlists';
      var fields = {token: token, page: next};
      if (playlist) { fields.id = playlist.id; }
      more.disabled = true;
      ui.message('account-more', '加载更多');
      ui.message('account-list-status', playlist ? '正在读取歌曲…' : '正在读取个人歌单…');
      call(action, fields, function (error, data, status) {
        if (revision !== listRevision || !opened) { return; }
        more.disabled = false;
        if (status === 401) {
          expire(error);
          return;
        }
        if (error) {
          ui.message('account-list-status', error);
          ui.message('account-more', '重试');
          ui.visible(more, true);
          return;
        }
        var fragment = document.createDocumentFragment();
        var base = tracks.length;
        var items = playlist ? data.tracks : data.playlist;
        if (playlist) { tracks = tracks.concat(items); }
        items.forEach(function (item, index) {
          if (!playlist) {
            fragment.appendChild(playlistRow(item));
            return;
          }
          var row = ui.trackRow(item, activeSong === item.id, function () {
            ui.play(tracks, base + index);
          });
          row.setAttribute('data-track-id', item.id);
          fragment.appendChild(row);
        });
        byId('account-list').appendChild(fragment);
        listPage = next;
        ui.message('account-list-status', items.length || next > 1 ? '' : (playlist ? '这个歌单还没有歌曲。' : '这个账号还没有歌单。'));
        ui.visible(more, data.more);
      });
    }

    function close() {
      if (!opened) { return; }
      opened = false;
      clearQR(true);
      resetList();
      ui.visible(byId('account-screen'), false);
      ui.layout();
    }

    function back() {
      if (playlist) {
        showLibrary();
        byId('account-back').focus();
      } else {
        close();
        byId('account-open').focus();
      }
    }

    function open() {
      opened = true;
      ui.visible(byId('account-screen'), true);
      refreshProfile();
      showLibrary();
      ui.layout();
      byId('account-back').focus();
    }

    function logout() {
      byId('account-logout').disabled = true;
      call('logout', {token: token}, function (error) {
        byId('account-logout').disabled = false;
        if (error) { ui.message('account-state', error); return; }
        expire('已退出登录。');
      });
    }

    byId('account-open').onclick = open;
    byId('account-back').onclick = back;
    byId('account-login').onclick = startLogin;
    byId('account-logout').onclick = logout;
    byId('account-more').onclick = loadPage;
    if (token) {
      var saved = token;
      call('status', {token: saved}, function (error, data) {
        if (token !== saved || pending) { return; }
        if (error) { return; }
        if (data.state === 'signed_in') {
          profile = data.profile;
          refreshProfile();
          if (opened) { showLibrary(); }
        } else {
          storeToken('');
        }
      });
    }

    return {
      opened: function () { return opened; },
      close: close,
      back: back,
      expired: expire,
      fetchPlay: function (id, done) {
        call('play', {token: token, id: id}, done);
      },
      signedIn: function () { return Boolean(profile); },
      select: function (track) {
        activeSong = track && track.source === 'netease' ? track.id : '';
        var rows = byId('account-list').getElementsByTagName('li');
        Array.prototype.forEach.call(rows, function (row) {
          var id = row.getAttribute('data-track-id');
          if (id) { row.firstChild.className = id === activeSong ? 'track selected' : 'track'; }
        });
      }
    };
  };
}());
