(function () {
  'use strict';

  var byId = function (id) { return document.getElementById(id); };
  var tabs = byId('sources').getElementsByTagName('button');
  var query = byId('query');
  var audio = byId('audio');
  var storeAudio = byId('store-audio');
  var sourceNames = { tencent: 'QQ 音乐', netease: '网易云', kuwo: '酷我', kugou: '酷狗', apple: 'Apple Music' };
  var preferenceKey = 'palmcadence.preferences';
  var defaults = { startupSource: 'last', lastSource: 'netease', mode: 'sequence', theme: 'dark', region: 'US' };
  var playbackHelp = {
    sequence: '播到列表末尾后停止',
    'repeat-all': '列表结束后从第一首重新播放',
    'repeat-one': '重复播放当前歌曲',
    stop: '播完当前歌曲就停止'
  };
  var fields = {
    'startup-source': { key: 'startupSource', allowed: ['last', 'netease', 'tencent', 'kuwo', 'kugou', 'apple'] },
    'playback-mode': { key: 'mode', allowed: ['sequence', 'repeat-all', 'repeat-one', 'stop'] },
    theme: { key: 'theme', allowed: ['dark', 'light'] },
    'apple-region': { key: 'region', allowed: ['US', 'CN', 'HK', 'TW', 'JP'] }
  };
  var preferences = readPreferences();
  var source = preferences.startupSource === 'last' ? preferences.lastSource : preferences.startupSource;
  var panel = 'results';
  var settingsOpen = false;
  var menuOpen = false;
  var activeChoice;
  var choiceButtons = {};
  var sleepDeadline = 0;
  var sleepAlarm;
  var resumeWhenReady = true;
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
  var searchRegion = preferences.region;

  function readPreferences() {
    var data;
    var prefs = {};
    try { data = JSON.parse(window.localStorage.getItem(preferenceKey)); } catch (error) {}
    Object.keys(defaults).forEach(function (key) { prefs[key] = defaults[key]; });
    if (!data || typeof data !== 'object') { return prefs; }
    Object.keys(fields).forEach(function (id) {
      var field = fields[id];
      if (field.allowed.indexOf(data[field.key]) !== -1) { prefs[field.key] = data[field.key]; }
    });
    if (Object.prototype.hasOwnProperty.call(sourceNames, data.lastSource)) { prefs.lastSource = data.lastSource; }
    return prefs;
  }

  function savePreferences() {
    try {
      window.localStorage.setItem(preferenceKey, JSON.stringify(preferences));
      return true;
    } catch (error) { return false; }
  }

  function syncLayout() {
    var classes = [];
    if (panel === 'results') { classes.push('show-library'); }
    if (preferences.theme === 'light') { classes.push('theme-light'); }
    if (settingsOpen) { classes.push('settings-open'); }
    document.body.className = classes.join(' ');
    document.body.setAttribute('data-panel', panel);
  }

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

  function showPanel(next) {
    if (panel === 'store-panel' && next !== panel) {
      storeVersion += 1;
      resetAudio(storeAudio);
    }
    panel = next;
    syncLayout();
    visible(byId('player'), panel !== 'store-panel');
    ['results', 'store-panel', 'now-panel'].forEach(function (name) {
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
    get('/api/play?source=apple&id=' + encodeURIComponent(track.id) + '&country=' + searchRegion, function (error, data) {
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
      searchRegion = preferences.region;
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
    get(catalogPath(source, q, nextPage, searchRegion), function (error, data) {
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

  function catalogPath(provider, term, pageNumber, region) {
    var path = '/api/search?source=' + provider + '&q=' + encodeURIComponent(term) + '&page=' + pageNumber;
    return provider === 'apple' ? path + '&country=' + region : path;
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
    resumeWhenReady = true;
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
      if (!resumeWhenReady) {
        message('player-status', '已定时暂停');
        return;
      }
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

  function updateSettings() {
    Object.keys(fields).forEach(function (id) { byId(id).value = preferences[fields[id].key]; });
    message('playback-help', playbackHelp[preferences.mode]);
    message('now-sleep', sleepDeadline ? '定时停止已设定' : '定时停止');
    if (sleepDeadline) {
      var end = new Date(sleepDeadline);
      var minutes = end.getMinutes();
      message('sleep-help', '将在 ' + end.getHours() + ':' + (minutes < 10 ? '0' : '') + minutes + ' 暂停');
    } else {
      byId('sleep-timer').value = '0';
      message('sleep-help', '到时暂停当前播放');
    }
    Object.keys(choiceButtons).forEach(function (id) {
      var select = byId(id);
      var value = select.options[select.selectedIndex].text;
      var button = choiceButtons[id];
      button.textContent = value;
      button.setAttribute('aria-label', select.parentNode.getElementsByTagName('label')[0].textContent + '，' + value);
    });
  }

  function closeChoice(restoreFocus) {
    if (!activeChoice) { return; }
    if (restoreFocus) { activeChoice.focus(); }
    activeChoice = null;
    visible(byId('choice-layer'), false);
  }

  function openChoice(select, label, trigger) {
    var items = byId('choice-items');
    var selectedButton;
    activeChoice = trigger;
    message('choice-title', label.textContent);
    items.innerHTML = '';
    Array.prototype.forEach.call(select.options, function (option) {
      var button = document.createElement('button');
      button.type = 'button';
      button.textContent = option.text;
      button.setAttribute('aria-pressed', option.selected ? 'true' : 'false');
      if (option.selected) { selectedButton = button; }
      button.onclick = function () {
        select.value = option.value;
        select.onchange();
        closeChoice(true);
      };
      items.appendChild(button);
    });
    visible(byId('choice-layer'), true);
    (selectedButton || byId('choice-close')).focus();
  }

  function addChoiceButton(id) {
    var select = byId(id);
    var label = select.parentNode.getElementsByTagName('label')[0];
    var button = document.createElement('button');
    button.type = 'button';
    button.className = 'choice-button';
    button.setAttribute('aria-haspopup', 'dialog');
    button.setAttribute('aria-describedby', select.getAttribute('aria-describedby') || '');
    button.onclick = function () { openChoice(select, label, button); };
    label.onclick = function () { button.onclick(); return false; };
    choiceButtons[id] = button;
    select.parentNode.appendChild(button);
    visible(select, false);
  }

  function setMenu(open) {
    if (open) { closeChoice(false); }
    menuOpen = open;
    visible(byId('menu-now'), currentIndex >= 0 || panel === 'store-panel');
    visible(byId('app-menu'), open);
    visible(byId('app-menu-backdrop'), open);
  }

  function showSettings() {
    if (settingsOpen) { return; }
    settingsOpen = true;
    updateSettings();
    visible(byId('settings'), true);
    byId('settings-scroll').scrollTop = 0;
    syncLayout();
    byId('settings-close').focus();
  }

  function closeSettings() {
    if (!settingsOpen) { return; }
    closeChoice(false);
    settingsOpen = false;
    visible(byId('settings'), false);
    syncLayout();
    if (panel === 'results') { showResults(); }
  }

  function showResults() {
    if (source === 'apple' && searchRegion !== preferences.region) { search(true); }
    else { showPanel('results'); }
  }

  function finishSleepTimer() {
    if (!sleepDeadline || Date.now() < sleepDeadline) { return false; }
    sleepDeadline = 0;
    clearTimeout(sleepAlarm);
    resumeWhenReady = false;
    audio.pause();
    storeAudio.pause();
    message('player-status', '已定时暂停');
    if (panel === 'store-panel') { message('store-status', '已定时暂停'); }
    updateSettings();
    return true;
  }

  function wakeSleepTimer() {
    if (!finishSleepTimer() && sleepDeadline) {
      sleepAlarm = setTimeout(wakeSleepTimer, Math.max(0, sleepDeadline - Date.now()));
    }
  }

  function checkConnection() {
    var provider = source;
    var region = preferences.region;
    byId('settings-retry').disabled = true;
    message('connection-status', '正在检查本机播放组件…');
    get('/api/health', function (error, data) {
      if (error || !data.ok) {
        message('connection-status', error || '本机播放组件未就绪。');
        byId('settings-retry').disabled = false;
        return;
      }
      message('connection-status', '正在连接 ' + sourceNames[provider] + '…');
      get(catalogPath(provider, 'Canon', 1, region), function (providerError) {
        message('connection-status', providerError ? sourceNames[provider] + '暂时无法访问。' : sourceNames[provider] + '连接正常。');
        byId('settings-retry').disabled = false;
      });
    });
  }

  Object.keys(fields).forEach(function (id) {
    byId(id).onchange = function () {
      var field = fields[id];
      if (field.allowed.indexOf(this.value) === -1) { return; }
      preferences[field.key] = this.value;
      message('settings-status', savePreferences() ? '' : '设置本次有效，设备暂时无法保存。');
      updateSettings();
      syncLayout();
    };
  });
  byId('sleep-timer').onchange = function () {
    var minutes = Number(this.value);
    clearTimeout(sleepAlarm);
    sleepDeadline = minutes ? Date.now() + minutes * 60000 : 0;
    updateSettings();
    wakeSleepTimer();
  };
  Object.keys(fields).concat('sleep-timer').forEach(addChoiceButton);
  updateSettings();
  byId('choice-close').onclick = byId('choice-backdrop').onclick = function () { closeChoice(true); };
  byId('search').onsubmit = function () { query.blur(); search(true); return false; };
  byId('more').onclick = function () { search(false); };
  byId('store-close').onclick = byId('now-close').onclick = showResults;
  byId('settings-button').onclick = showSettings;
  byId('settings-close').onclick = closeSettings;
  byId('settings-retry').onclick = checkConnection;
  byId('now-sleep').onclick = function () { showSettings(); choiceButtons['sleep-timer'].onclick(); };
  byId('app-menu-backdrop').onclick = function () { setMenu(false); };
  byId('menu-search').onclick = function () {
    setMenu(false);
    closeSettings();
    showResults();
    query.focus();
  };
  byId('menu-now').onclick = function () {
    setMenu(false);
    closeSettings();
    if (panel !== 'store-panel') { showPanel('now-panel'); }
  };
  byId('menu-settings').onclick = function () { setMenu(false); showSettings(); };
  byId('playing').onclick = function () {
    if (settingsOpen) { closeSettings(); }
    if (panel === 'now-panel') { showResults(); }
    else { showPanel('now-panel'); }
  };
  document.addEventListener('keydown', function (event) {
    if (event.keyCode !== 27 && event.keyCode !== 461) { return; }
    if (activeChoice) { closeChoice(true); }
    else if (menuOpen) { setMenu(false); }
    else if (settingsOpen) { closeSettings(); }
    else { return; }
    event.preventDefault();
  }, false);
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
  audio.addEventListener('timeupdate', function () { finishSleepTimer(); updateProgress(); }, false);
  storeAudio.addEventListener('timeupdate', finishSleepTimer, false);
  audio.addEventListener('durationchange', updateProgress, false);
  audio.addEventListener('playing', function () {
    if (!finishSleepTimer()) { playing(true); message('player-status', ''); }
  }, false);
  storeAudio.addEventListener('playing', function () {
    if (!finishSleepTimer()) { message('store-status', ''); }
  }, false);
  audio.addEventListener('pause', function () { playing(false); }, false);
  audio.addEventListener('ended', function () {
    playing(false);
    if (finishSleepTimer() || preferences.mode === 'stop') { return; }
    if (preferences.mode === 'repeat-one') {
      audio.currentTime = 0;
      playAudio();
    } else if (currentIndex + 1 < queue.length) {
      playAt(currentIndex + 1);
    } else if (preferences.mode === 'repeat-all') {
      playAt(0);
    }
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
      preferences.lastSource = source;
      savePreferences();
      updateTabs();
      search(true);
    };
  }
  if (preferences.lastSource !== source) {
    preferences.lastSource = source;
    savePreferences();
  }
  updateTabs();
  syncLayout();
  if (window.PalmSystem) {
    window.Mojo = window.Mojo || {};
    // Luna sends the title-bar menu action as a relaunch on legacy webOS.
    window.Mojo.relaunch = function () {
      var args = window.PalmSystem.launchParams;
      if (typeof args === 'string') {
        try { args = JSON.parse(args); } catch (error) { return false; }
      }
      if (!args || args['palm-command'] !== 'open-app-menu') { return false; }
      setMenu(!menuOpen);
      return true;
    };
    window.Mojo.stageDeactivated = function () { closeChoice(false); setMenu(false); };
    if (window.innerWidth >= 700 && window.PalmSystem.setWindowOrientation) {
      window.PalmSystem.setWindowOrientation('free');
    }
    if (window.PalmSystem.stageReady) { window.PalmSystem.stageReady(); }
  }
}());
