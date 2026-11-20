(function () {
  'use strict';

  var byId = function (id) { return document.getElementById(id); };
  var tabs = byId('sources').getElementsByTagName('button');
  var query = byId('query');
  var audio = byId('audio');
  var storeAudio = byId('store-audio');
  var sourceNames = { tencent: 'QQ 音乐', netease: '网易云', kuwo: '酷我', kugou: '酷狗', apple: 'Apple Music' };
  var source = 'netease';
  var origin = /^https?:$/.test(location.protocol) ? location.protocol + '//' + location.host : 'http://10.0.2.2:8787';
  var base = origin;
  var tracks = [];
  var queue = [];
  var currentIndex = -1;
  var searchVersion = 0;
  var playbackVersion = 0;
  var storeVersion = 0;
  var page = 1;
  var hasMore = false;
  var keyword = '';
  var requested = false;
  var mediaPath = '';

  try { base = localStorage.getItem('music-server') || origin; } catch (error) {}
  function visible(node, show) { node.className = show ? '' : 'hidden'; }
  function message(id, text) { byId(id).textContent = text; }

  function get(server, path, callback) {
    var xhr = new XMLHttpRequest();
    var done = false;
    var timer = setTimeout(function () {
      complete('连接超时，请检查音乐服务。');
      xhr.abort();
    }, 15000);
    function complete(error, data) {
      if (done) { return; }
      done = true;
      clearTimeout(timer);
      callback(error, data);
    }
    try {
      xhr.open('GET', server + path, true);
      xhr.onreadystatechange = function () {
        var data;
        if (xhr.readyState !== 4 || done) { return; }
        if (!xhr.status) {
          complete('无法连接到音乐服务。');
          return;
        }
        try { data = JSON.parse(xhr.responseText); }
        catch (error) { complete('音乐服务响应无法读取。'); return; }
        if (xhr.status !== 200) {
          complete(data.message || '请求失败。');
          return;
        }
        complete('', data);
      };
      xhr.send();
    } catch (error) {
      complete('无法连接到音乐服务。');
    }
  }

  function resetAudio(element) {
    element.pause();
    element.removeAttribute('src');
    element.load();
  }

  function closeStore() {
    storeVersion += 1;
    visible(byId('store-panel'), false);
    visible(byId('results'), true);
    resetAudio(storeAudio);
  }

  function showStore(track) {
    var version = ++storeVersion;
    audio.pause();
    resetAudio(storeAudio);
    visible(byId('results'), false);
    visible(byId('store-panel'), true);
    visible(byId('store-audio'), false);
    visible(byId('store-preview-info'), false);
    visible(byId('store-badge'), false);
    message('store-title', track.title);
    message('store-artist', track.artist + (track.album ? ' · ' + track.album : ''));
    message('store-status', '');
    byId('store-link').href = track.url;
    if (!track.preview) { return; }
    get(base, '/api/play?source=apple&id=' + encodeURIComponent(track.id), function (error, data) {
      if (version !== storeVersion) { return; }
      if (error) { message('store-status', error); return; }
      byId('store-badge').href = track.url;
      visible(byId('store-preview-info'), true);
      visible(byId('store-badge'), true);
      visible(byId('store-audio'), true);
      storeAudio.src = base + data.path;
    });
  }

  function updateTabs() {
    var index, tab, active;
    for (index = 0; index < tabs.length; index += 1) {
      tab = tabs[index];
      active = tab.getAttribute('data-source') === source;
      tab.className = active ? 'active' : '';
      tab.setAttribute('aria-pressed', active ? 'true' : 'false');
    }
  }

  function appendText(parent, tag, className, text) {
    var element = document.createElement(tag);
    element.className = className;
    element.textContent = text;
    parent.appendChild(element);
  }

  function renderTracks() {
    var list = byId('tracks');
    var fragment = document.createDocumentFragment();
    var current = queue[currentIndex];
    list.innerHTML = '';
    tracks.forEach(function (track, index) {
      var row = document.createElement('li');
      var button = document.createElement('button');
      button.type = 'button';
      button.className = current && current.id === track.id && current.source === track.source ? 'track selected' : 'track';
      appendText(button, 'span', 'track-title', track.title);
      appendText(button, 'span', 'track-info', track.artist + (track.album ? ' · ' + track.album : ''));
      appendText(button, 'span', 'track-action', track.source === 'apple' ? '›' : '▶');
      button.onclick = function () {
        if (track.source === 'apple') {
          showStore(track);
        } else {
          queue = tracks.slice(0);
          playAt(index);
        }
      };
      row.appendChild(button);
      fragment.appendChild(row);
    });
    list.appendChild(fragment);
    visible(byId('more'), hasMore && !requested);
  }

  function search(reset) {
    var version, nextPage, q;
    if (reset) {
      closeStore();
      keyword = query.value.replace(/^\s+|\s+$/g, '');
      tracks = [];
      page = 1;
      hasMore = false;
      byId('results').scrollTop = 0;
    }
    version = ++searchVersion;
    q = keyword;
    nextPage = reset ? 1 : page + 1;
    requested = Boolean(q);
    renderTracks();
    if (!q) { message('search-status', '输入歌名或歌手开始搜索。'); return; }
    message('search-status', '正在搜索 ' + sourceNames[source] + '…');
    get(base, '/api/search?source=' + source + '&q=' + encodeURIComponent(q) + '&page=' + nextPage, function (error, data) {
      if (version !== searchVersion) { return; }
      requested = false;
      if (error) {
        message('search-status', error);
      } else {
        page = nextPage;
        tracks = tracks.concat(data.tracks);
        hasMore = data.more;
        message('search-status', tracks.length ? '' : '当前平台没有找到相关曲目。');
      }
      renderTracks();
    });
  }

  function formatTime(seconds) {
    seconds = isFinite(seconds) && seconds > 0 ? Math.floor(seconds) : 0;
    var remainder = seconds % 60;
    return Math.floor(seconds / 60) + ':' + (remainder < 10 ? '0' : '') + remainder;
  }

  function updateProgress() {
    var duration = mediaPath && isFinite(audio.duration) ? audio.duration : 0;
    var current = mediaPath ? audio.currentTime : 0;
    var percent = duration > 0 && isFinite(current) ? Math.min(100, Math.round(current * 100 / duration)) : 0;
    byId('progress-fill').style.width = percent + '%';
    byId('progress').setAttribute('aria-valuenow', percent);
    message('player-time', formatTime(current) + ' / ' + formatTime(duration));
  }

  function playAudio() {
    var promise;
    try {
      promise = audio.play();
      if (promise && promise.then) {
        promise.then(null, function (error) {
          if (error.name === 'NotAllowedError') { message('player-status', '点「播放」开始。'); }
        });
      }
    } catch (error) {
      message('player-status', '当前设备无法播放此音频。');
    }
  }

  function playAt(index) {
    var track = queue[index];
    var version;
    if (!track) { return; }
    version = ++playbackVersion;
    currentIndex = index;
    mediaPath = '';
    resetAudio(audio);
    updateProgress();
    byId('provider-link').href = track.url;
    visible(byId('provider-link'), true);
    message('now-title', track.title);
    message('now-info', track.artist + ' · ' + sourceNames[track.source]);
    message('toggle', '播放');
    message('player-status', '正在获取音源…');
    byId('toggle').disabled = true;
    byId('previous').disabled = index === 0;
    byId('next').disabled = index >= queue.length - 1;
    renderTracks();
    get(base, '/api/play?source=' + track.source + '&id=' + encodeURIComponent(track.id), function (error, data) {
      if (version !== playbackVersion) { return; }
      if (error) { message('player-status', error); return; }
      mediaPath = data.path;
      audio.src = base + mediaPath;
      byId('toggle').disabled = false;
      message('player-status', '正在缓冲…');
      playAudio();
    });
  }

  byId('search').onsubmit = function () { query.blur(); search(true); return false; };
  byId('more').onclick = function () { search(false); };
  byId('store-close').onclick = closeStore;
  byId('settings-button').onclick = function () {
    closeStore();
    byId('server').value = base;
    message('connection-status', '');
    visible(byId('results'), false);
    visible(byId('settings'), true);
  };
  byId('settings-close').onclick = function () {
    visible(byId('settings'), false);
    visible(byId('results'), true);
  };
  byId('settings').onsubmit = function () {
    var entered = byId('server').value.replace(/^\s+|\s+$/g, '').replace(/\/$/, '');
    if (!/^https?:\/\/[a-z0-9.-]+(?::\d{1,5})?$/i.test(entered)) {
      message('connection-status', '请输入 http://地址:端口。');
      return false;
    }
    message('connection-status', '连接中…');
    get(entered, '/api/health', function (error, data) {
      if (error || !data.ok) { message('connection-status', error || '服务无响应。'); return; }
      if (base !== entered) {
        playbackVersion += 1;
        mediaPath = '';
        resetAudio(audio);
        updateProgress();
        queue = [];
        currentIndex = -1;
        byId('previous').disabled = true;
        byId('next').disabled = true;
        byId('toggle').disabled = true;
        visible(byId('provider-link'), false);
        message('now-title', '未播放');
        message('now-info', '');
        message('player-status', '');
      }
      base = entered;
      try { localStorage.setItem('music-server', base); } catch (exception) {}
      byId('settings-close').onclick();
      search(true);
    });
    return false;
  };
  byId('toggle').onclick = function () { if (audio.paused) { playAudio(); } else { audio.pause(); } };
  byId('previous').onclick = function () { playAt(currentIndex - 1); };
  byId('next').onclick = function () { playAt(currentIndex + 1); };
  byId('progress').onclick = function (event) {
    var bounds = this.getBoundingClientRect();
    if (mediaPath && isFinite(audio.duration) && audio.duration > 0) {
      audio.currentTime = Math.max(0, Math.min(audio.duration, (event.clientX - bounds.left) * audio.duration / (bounds.right - bounds.left)));
    }
  };
  byId('progress').onkeydown = function (event) {
    if (mediaPath && isFinite(audio.duration) && (event.keyCode === 37 || event.keyCode === 39)) {
      audio.currentTime = Math.max(0, Math.min(audio.duration, audio.currentTime + (event.keyCode === 37 ? -5 : 5)));
      event.preventDefault();
    }
  };
  audio.addEventListener('timeupdate', updateProgress, false);
  audio.addEventListener('durationchange', updateProgress, false);
  audio.addEventListener('playing', function () { message('toggle', '暂停'); message('player-status', ''); }, false);
  audio.addEventListener('pause', function () { message('toggle', '播放'); }, false);
  audio.addEventListener('ended', function () {
    message('toggle', '播放');
    if (currentIndex + 1 < queue.length) { playAt(currentIndex + 1); }
  }, false);
  audio.addEventListener('error', function () {
    if (mediaPath) { message('player-status', '音频加载失败，可重试或打开原平台。'); }
  }, false);
  storeAudio.addEventListener('error', function () {
    if (storeAudio.getAttribute('src')) { message('store-status', '试听暂时无法加载。'); }
  }, false);
  for (var index = 0; index < tabs.length; index += 1) {
    tabs[index].onclick = function () {
      if (this.getAttribute('data-source') === source) { return; }
      source = this.getAttribute('data-source');
      updateTabs();
      search(true);
    };
  }
  updateTabs();
  if (window.PalmSystem && window.PalmSystem.stageReady) { window.PalmSystem.stageReady(); }
}());
