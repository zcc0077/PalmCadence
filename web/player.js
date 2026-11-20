(function () {
  'use strict';

  var byId = function (id) { return document.getElementById(id); };
  var tabs = byId('sources').getElementsByTagName('button');
  var query = byId('query');
  var audio = byId('audio');
  var storeAudio = byId('store-audio');
  var sourceNames = { tencent: 'QQ 音乐', netease: '网易云', kuwo: '酷我', kugou: '酷狗', apple: 'Apple Music' };
  var source = 'netease';
  var origin = /^https?:$/.test(location.protocol) ? location.protocol + '//' + location.host : 'http://127.0.0.1:8787';
  var tracks = [];
  var queue = [];
  var currentIndex = -1;
  var searchVersion = 0;
  var playbackVersion = 0;
  var storeVersion = 0;
  var playbackWatchdog;
  var page = 1;
  var hasMore = false;
  var keyword = '';
  var requested = false;
  var mediaPath = '';

  function visible(node, show) {
    var classes = (node.className || '').replace(/(^|\s)hidden(?=\s|$)/g, '').replace(/^\s+|\s+$/g, '');
    node.className = show ? classes : classes + ' hidden';
  }
  function message(id, text) {
    byId(id).textContent = text;
    if (id === 'player-status') { byId('detail-status').textContent = text; }
    if (id === 'search-status') { visible(byId(id), Boolean(text)); }
  }
  function playing(yes) {
    var toggle = byId('toggle');
    var label = yes ? '暂停' : '播放';
    toggle.className = yes ? 'is-playing' : '';
    toggle.setAttribute('aria-label', label);
    toggle.title = label;
  }

  function get(path, callback) {
    var xhr = new XMLHttpRequest();
    var done = false;
    var timer = setTimeout(function () {
      complete('连接超时，请检查网络。');
      xhr.abort();
    }, 15000);
    function complete(error, data) {
      if (done) { return; }
      done = true;
      clearTimeout(timer);
      callback(error, data);
    }
    try {
      xhr.open('GET', origin + path, true);
      xhr.onreadystatechange = function () {
        var data;
        if (xhr.readyState !== 4 || done) { return; }
        if (!xhr.status) {
          complete('无法连接到本机播放组件。');
          return;
        }
        try { data = JSON.parse(xhr.responseText); }
        catch (error) { complete('无法读取曲目数据。'); return; }
        if (xhr.status !== 200) {
          complete(data.message || '请求失败。');
          return;
        }
        complete('', data);
      };
      xhr.send();
    } catch (error) {
      complete('无法连接到本机播放组件。');
    }
  }

  function resetAudio(element) {
    element.pause();
    element.removeAttribute('src');
    element.load();
  }

  function showPanel(panel) {
    if (panel !== 'store-panel') {
      storeVersion += 1;
      resetAudio(storeAudio);
    }
    document.body.className = panel === 'results' ? 'show-library' : '';
    visible(byId('player'), panel !== 'store-panel');
    ['results', 'store-panel', 'now-panel', 'settings'].forEach(function (name) {
      visible(byId(name), name === panel);
    });
  }

  function showStore(track) {
    var version = ++storeVersion;
    audio.pause();
    resetAudio(storeAudio);
    showPanel('store-panel');
    visible(byId('store-audio'), false);
    visible(byId('store-preview-info'), false);
    visible(byId('store-badge'), false);
    message('store-title', track.title);
    message('store-artist', track.artist + (track.album ? ' · ' + track.album : ''));
    message('store-status', '');
    byId('store-link').href = track.url;
    if (!track.preview) {
      message('store-status', '此曲目没有试听音频。');
      return;
    }
    message('store-status', '正在加载试听…');
    get('/api/play?source=apple&id=' + encodeURIComponent(track.id), function (error, data) {
      if (version !== storeVersion) { return; }
      if (error) { message('store-status', error); return; }
      message('store-status', '');
      byId('store-badge').href = track.url;
      visible(byId('store-preview-info'), true);
      visible(byId('store-badge'), true);
      visible(byId('store-audio'), true);
      storeAudio.src = origin + data.path;
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
      var artwork = document.createElement('span');
      artwork.className = 'track-art';
      artwork.setAttribute('aria-hidden', 'true');
      artwork.setAttribute('data-source', track.source);
      button.appendChild(artwork);
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
    visible(byId('empty-state'), !keyword);
    visible(byId('results-heading'), Boolean(tracks.length));
  }

  function search(reset) {
    var version, nextPage, q;
    if (reset) {
      showPanel('results');
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
    if (!q) { message('search-status', ''); return; }
    message('search-status', '正在搜索 ' + sourceNames[source] + '…');
    get('/api/search?source=' + source + '&q=' + encodeURIComponent(q) + '&page=' + nextPage, function (error, data) {
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
    var timeLabel = formatTime(current) + ' / ' + formatTime(duration);
    byId('progress').setAttribute('aria-valuetext', timeLabel);
    message('player-time', timeLabel);
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
    var version, details;
    if (!track) { return; }
    version = ++playbackVersion;
    clearTimeout(playbackWatchdog);
    currentIndex = index;
    mediaPath = '';
    resetAudio(audio);
    updateProgress();
    byId('provider-link').href = track.url;
    visible(byId('provider-link'), true);
    message('now-title', track.title);
    message('now-info', track.artist + ' · ' + sourceNames[track.source]);
    details = track.artist + (track.album ? ' · ' + track.album : '') + ' · ' + sourceNames[track.source];
    message('detail-title', track.title);
    message('detail-info', details);
    message('tablet-title', track.title);
    message('tablet-info', details);
    byId('now-art').setAttribute('data-source', track.source);
    byId('playing').disabled = false;
    playing(false);
    message('player-status', '正在获取音源…');
    byId('toggle').disabled = true;
    byId('previous').disabled = index === 0;
    byId('next').disabled = index >= queue.length - 1;
    renderTracks();
    get('/api/play?source=' + track.source + '&id=' + encodeURIComponent(track.id), function (error, data) {
      if (version !== playbackVersion) { return; }
      if (error) { message('player-status', error); return; }
      mediaPath = data.path;
      audio.src = origin + mediaPath;
      audio.load();
      byId('toggle').disabled = false;
      message('player-status', '正在缓冲…');
      playAudio();
      playbackWatchdog = setTimeout(function () {
        if (version === playbackVersion && audio.readyState < 2 &&
            byId('player-status').textContent === '正在缓冲…') {
          message('player-status', '音频未能开始，可重试或打开原平台。');
        }
      }, 18000);
    });
  }

  byId('search').onsubmit = function () { query.blur(); search(true); return false; };
  byId('more').onclick = function () { search(false); };
  byId('store-close').onclick = byId('now-close').onclick = byId('settings-close').onclick = function () {
    showPanel('results');
  };
  byId('playing').onclick = function () {
    var panel = byId('now-panel');
    showPanel(panel.className.indexOf('hidden') === -1 ? 'results' : 'now-panel');
  };
  function checkConnection() {
    message('connection-status', '正在检查本机播放组件…');
    get('/api/health', function (error, data) {
      message('connection-status', error || (data.ok ? '本机播放组件已就绪。' : '本机播放组件未就绪。'));
    });
  }
  byId('settings-button').onclick = function () { showPanel('settings'); checkConnection(); };
  byId('settings-retry').onclick = checkConnection;
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
  audio.addEventListener('playing', function () { playing(true); message('player-status', ''); }, false);
  audio.addEventListener('pause', function () { playing(false); }, false);
  audio.addEventListener('ended', function () {
    playing(false);
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
  if (window.PalmSystem) {
    if (window.innerWidth >= 700 && window.PalmSystem.setWindowOrientation) {
      window.PalmSystem.setWindowOrientation('free');
    }
    if (window.PalmSystem.stageReady) { window.PalmSystem.stageReady(); }
  }
}());
