(function () {
  'use strict';

  var byId = function (id) { return document.getElementById(id); };
  var tabs = byId('sources').getElementsByTagName('button');
  var query = byId('query');
  var audio = byId('audio');
  var storeAudio = byId('store-audio');
  var sourceNames = { tencent: 'QQ 音乐', netease: '网易云', kuwo: '酷我', kugou: '酷狗', apple: 'Apple Music' };
  var preferenceKey = 'palmcadence.preferences';
  var defaults = { startupSource: 'last', lastSource: 'netease', mode: 'sequence', theme: 'dark', region: 'US', fade: 'off' };
  var playbackHelp = {
    sequence: '播到列表末尾后停止',
    'repeat-all': '列表结束后从第一首重新播放',
    'repeat-one': '重复播放当前歌曲',
    stop: '播完当前歌曲就停止'
  };
  var fields = {
    'startup-source': { key: 'startupSource', allowed: ['last', 'netease', 'tencent', 'kuwo', 'kugou', 'apple'] },
    'playback-mode': { key: 'mode', allowed: ['sequence', 'repeat-all', 'repeat-one', 'stop'] },
    fade: { key: 'fade', allowed: ['off', 'on'] },
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
  var cacheRefresh;
  var cacheRead = 0;
  var cacheRevision = 0;
  var cacheBusy = false;
  var cacheReady = false;
  var sleepDeadline = 0;
  var sleepAlarm;
  var resumeWhenReady = true;
  var origin = /^https?:$/.test(location.protocol) ? location.protocol + '//' + location.host : 'http://127.0.0.1:8787';
  var tracks = [];
  var queue = [];
  var personalQueue = false;
  var playbackAuthenticated = false;
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
  var trackFade = mediaFade(audio);
  var previewFade = mediaFade(storeAudio);
  var account = window.createPalmAccount({
    request: get, visible: visible, message: message, trackRow: trackRow,
    play: playPersonal, stopPlayback: stopPersonalPlayback, layout: syncLayout
  });

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
    if (settingsOpen || (account && account.opened())) { classes.push('settings-open'); }
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

  function get(path, callback, method, body) {
    var xhr = new XMLHttpRequest();
    var done = false;
    var timer = setTimeout(function () {
      complete('连接超时，请检查网络。');
      xhr.abort();
    }, 15000);
    function complete(error, data, status) {
      if (done) { return; }
      done = true;
      clearTimeout(timer);
      callback(error, data, status);
    }
    try {
      xhr.open(method || 'GET', origin + path, true);
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
          complete(data.message || '请求失败。', null, xhr.status);
          return;
        }
        complete('', data);
      };
      if (typeof body === 'string') { xhr.setRequestHeader('Content-Type', 'application/x-www-form-urlencoded'); }
      xhr.send(typeof body === 'string' ? body : null);
    } catch (error) {
      complete('无法连接到本机播放组件。');
    }
  }

  function setVolume(element, value) {
    try { element.volume = value; return element.volume === value; }
    catch (error) { return false; }
  }

  function mediaFade(element) {
    var ceiling = 1;
    var timer;
    var waiting = false;
    var pausing = false;

    function cancel(restore) {
      clearInterval(timer);
      timer = null;
      waiting = false;
      pausing = false;
      if (restore) { setVolume(element, ceiling); }
    }

    function ramp(target, pauseAtEnd) {
      var from = element.volume;
      var started = Date.now();
      cancel(false);
      pausing = pauseAtEnd;
      timer = setInterval(function () {
        var progress = Math.min(1, (Date.now() - started) / 350);
        if (!setVolume(element, from + (target - from) * progress) || progress === 1) {
          if (pauseAtEnd) { element.pause(); }
          cancel(true);
        }
      }, 35);
    }

    element.addEventListener('playing', function () {
      if (waiting) { ramp(ceiling, false); }
    }, false);
    ['pause', 'ended', 'error'].forEach(function (name) {
      element.addEventListener(name, function () {
        if (waiting || timer) { cancel(true); }
      }, false);
    });

    return {
      play: function () {
        if (pausing && !element.paused) { ramp(ceiling, false); return; }
        if (element.paused && preferences.fade === 'on') {
          ceiling = element.volume;
          waiting = ceiling > 0 && setVolume(element, 0);
        }
        return element.play();
      },
      pause: function () {
        if (pausing) { return; }
        if (!element.paused && preferences.fade === 'on' && element.volume > 0) {
          if (!waiting && !timer) { ceiling = element.volume; }
          ramp(0, true);
          return;
        }
        element.pause();
        if (waiting || timer) { cancel(true); }
      },
      shouldPlay: function () { return element.paused || pausing; },
      reset: function () {
        if (pausing) { element.pause(); }
        if (waiting || timer) { cancel(true); }
      }
    };
  }

  function resetAudio(element, fade) {
    fade.reset();
    element.pause();
    element.removeAttribute('src');
    element.load();
  }

  function showPanel(next) {
    if (panel === 'store-panel' && next !== panel) {
      storeVersion += 1;
      resetAudio(storeAudio, previewFade);
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
    trackFade.reset();
    audio.pause();
    resetAudio(storeAudio, previewFade);
    showPanel('store-panel');
    visible(byId('store-controls'), false);
    updateStoreProgress();
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
      visible(byId('store-controls'), true);
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

  function trackRow(track, selected, activate) {
    var row = document.createElement('li');
    var button = document.createElement('button');
    var artwork = document.createElement('span');
    artwork.className = 'track-art';
    artwork.setAttribute('aria-hidden', 'true');
    artwork.setAttribute('data-source', track.source);
    button.appendChild(artwork);
    button.type = 'button';
    button.className = selected ? 'track selected' : 'track';
    appendText(button, 'span', 'track-title', track.title);
    appendText(button, 'span', 'track-info', track.artist + (track.album ? ' · ' + track.album : ''));
    appendText(button, 'span', 'track-action', track.source === 'apple' ? '›' : '▶');
    button.onclick = activate;
    row.appendChild(button);
    return row;
  }

  function renderTracks() {
    var list = byId('tracks');
    var fragment = document.createDocumentFragment();
    var current = queue[currentIndex];
    list.innerHTML = '';
    tracks.forEach(function (track, index) {
      var selected = current && current.id === track.id && current.source === track.source;
      fragment.appendChild(trackRow(track, selected, function () {
        if (track.source === 'apple') {
          showStore(track);
        } else {
          personalQueue = false;
          queue = tracks.slice(0);
          playAt(index);
        }
      }));
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

  function renderProgress(id, timeId, element, ready) {
    var duration = ready && isFinite(element.duration) ? element.duration : 0;
    var current = ready ? element.currentTime : 0;
    var percent = duration > 0 && isFinite(current) ? Math.min(100, Math.round(current * 100 / duration)) : 0;
    byId(id + '-fill').style.width = percent + '%';
    byId(id).setAttribute('aria-valuenow', percent);
    var timeLabel = formatTime(current) + ' / ' + formatTime(duration);
    byId(id).setAttribute('aria-valuetext', timeLabel);
    message(timeId, timeLabel);
  }

  function updateProgress() { renderProgress('progress', 'player-time', audio, Boolean(mediaPath)); }
  function updateStoreProgress() { renderProgress('store-progress', 'store-time', storeAudio, Boolean(storeAudio.getAttribute('src'))); }

  function playMedia(fade, status) {
    var promise;
    try {
      promise = fade.play();
      if (promise && promise.then) {
        promise.then(null, function (error) {
          if (error.name === 'NotAllowedError') {
            fade.reset();
            message(status, '点「播放」开始。');
          }
        });
      }
    } catch (error) {
      fade.reset();
      message(status, '当前设备无法播放此音频。');
    }
  }

  function playAudio() { playMedia(trackFade, 'player-status'); }

  function playPersonal(items, index) {
    if (panel === 'store-panel') { showPanel('results'); }
    personalQueue = true;
    queue = items.slice(0);
    playAt(index);
  }

  function stopPersonalPlayback() {
    if (!personalQueue && !playbackAuthenticated) { return; }
    playbackVersion += 1;
    clearTimeout(playbackWatchdog);
    resetAudio(audio, trackFade);
    mediaPath = '';
    currentIndex = -1;
    queue = [];
    personalQueue = playbackAuthenticated = false;
    ['playing', 'toggle', 'previous', 'next'].forEach(function (id) { byId(id).disabled = true; });
    visible(byId('provider-link'), false);
    byId('now-art').removeAttribute('data-source');
    message('now-title', '未播放');
    message('now-info', '从搜索结果选择曲目');
    message('detail-title', '未播放');
    message('detail-info', '');
    message('tablet-title', '未播放');
    message('tablet-info', '');
    message('player-status', '');
    updateProgress();
    renderTracks();
    account.select(null);
    if (panel === 'now-panel') { showPanel('results'); }
  }

  function playAt(index) {
    var track = queue[index];
    var version, details;
    if (!track) { return; }
    version = ++playbackVersion;
    resumeWhenReady = true;
    clearTimeout(playbackWatchdog);
    currentIndex = index;
    playbackAuthenticated = track.source === 'netease' && account.signedIn();
    account.select(track);
    mediaPath = '';
    resetAudio(audio, trackFade);
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
    function loadTrack(error, data) {
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
    }
    function guestStream() {
      get('/api/play?source=' + track.source + '&id=' + encodeURIComponent(track.id), loadTrack);
    }
    if (playbackAuthenticated) {
      account.fetchPlay(track.id, function (error, data, status) {
        if (version !== playbackVersion) { return; }
        if (status === 401) {
          playbackAuthenticated = false;
          account.expired(error);
          if (version === playbackVersion) { guestStream(); }
          return;
        }
        loadTrack(error, data);
      });
    } else {
      guestStream();
    }
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
    button.onclick = function () { if (!button.disabled) { openChoice(select, label, button); } };
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
    account.close();
    settingsOpen = true;
    updateSettings();
    visible(byId('settings'), true);
    byId('settings-scroll').scrollTop = 0;
    syncLayout();
    byId('settings-close').focus();
    refreshCache();
    cacheRefresh = setInterval(refreshCache, 4000);
  }

  function closeSettings() {
    if (!settingsOpen) { return; }
    closeChoice(false);
    account.close();
    clearInterval(cacheRefresh);
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
    trackFade.pause();
    previewFade.pause();
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

  function refreshCache() {
    if (!cacheBusy && cacheRead === 0) { requestCache(''); }
  }

  function formatBytes(bytes) {
    if (bytes >= 1073741824) { return (bytes / 1073741824).toFixed(1) + ' GB'; }
    if (bytes >= 1048576) { return (bytes / 1048576).toFixed(1) + ' MB'; }
    return bytes > 0 ? Math.ceil(bytes / 1024) + ' KB' : '0 MB';
  }

  function requestCache(action) {
    var limit = byId('cache-limit');
    var trigger = choiceButtons['cache-limit'];
    var clear = byId('cache-clear');
    var revision = ++cacheRevision;
    if (action) {
      cacheBusy = true;
      trigger.disabled = clear.disabled = true;
      message('cache-status', action === 'clear=1' ? '正在清理…' : '正在保存上限…');
    } else {
      cacheRead = revision;
    }
    get('/api/cache' + (action ? '?' + action : ''), function (error, data) {
      if (cacheRead === revision) { cacheRead = 0; }
      if (revision !== cacheRevision) { return; }
      if (action) { cacheBusy = false; }
      if (error) {
        if (!action) {
          cacheReady = false;
          message('cache-usage', '暂时无法读取使用量');
          visible(byId('cache-space'), false);
        }
        message('cache-status', error);
        if (action) { limit.value = limit.getAttribute('data-saved'); }
      } else {
        cacheReady = true;
        limit.value = String(data.limit);
        limit.setAttribute('data-saved', limit.value);
        message('cache-usage', '已用 ' + formatBytes(data.bytes) + ' · ' + data.count + ' 首');
        message('cache-space', '设备可用 ' + formatBytes(data.free));
        visible(byId('cache-space'), data.free >= 0);
        if (action) { message('cache-status', action === 'clear=1' ? '缓存已清理' : '缓存上限已保存'); }
      }
      trigger.disabled = clear.disabled = !cacheReady;
      updateSettings();
    }, action ? 'POST' : 'GET');
  }

  Object.keys(fields).forEach(function (id) {
    byId(id).onchange = function () {
      var field = fields[id];
      if (field.allowed.indexOf(this.value) === -1) { return; }
      preferences[field.key] = this.value;
      if (id === 'fade' && this.value === 'off') { trackFade.reset(); previewFade.reset(); }
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
  byId('cache-limit').onchange = function () {
    if (!cacheBusy && cacheReady) { requestCache('limit=' + encodeURIComponent(this.value)); }
  };
  byId('cache-clear').onclick = function () { if (!cacheBusy) { requestCache('clear=1'); } };
  Object.keys(fields).concat('sleep-timer', 'cache-limit').forEach(addChoiceButton);
  choiceButtons['cache-limit'].disabled = true;
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
  byId('menu-now').onclick = function () {
    setMenu(false);
    account.close();
    closeSettings();
    if (panel !== 'store-panel') { showPanel('now-panel'); }
  };
  byId('menu-settings').onclick = function () { setMenu(false); showSettings(); };
  byId('playing').onclick = function () {
    account.close();
    if (settingsOpen) { closeSettings(); }
    if (panel === 'now-panel') { showResults(); }
    else { showPanel('now-panel'); }
  };
  document.addEventListener('keydown', function (event) {
    if (event.keyCode !== 27 && event.keyCode !== 461) { return; }
    if (activeChoice) { closeChoice(true); }
    else if (menuOpen) { setMenu(false); }
    else if (account.opened()) { account.back(); }
    else if (settingsOpen) { closeSettings(); }
    else { return; }
    event.preventDefault();
  }, false);
  byId('toggle').onclick = function () { if (trackFade.shouldPlay()) { playAudio(); } else { trackFade.pause(); } };
  byId('store-toggle').onclick = function () {
    if (previewFade.shouldPlay()) { playMedia(previewFade, 'store-status'); }
    else { previewFade.pause(); }
  };
  byId('previous').onclick = function () { playAt(currentIndex - 1); };
  byId('next').onclick = function () { playAt(currentIndex + 1); };
  function bindSeek(element, slider) {
    function seek(time) {
      if (isFinite(element.duration) && element.duration > 0) {
        element.currentTime = Math.max(0, Math.min(element.duration, time));
      }
    }
    slider.onclick = function (event) {
      var bounds = this.getBoundingClientRect();
      seek((event.clientX - bounds.left) * element.duration / (bounds.right - bounds.left));
    };
    slider.onkeydown = function (event) {
      if (event.keyCode === 37 || event.keyCode === 39) {
        seek(element.currentTime + (event.keyCode === 37 ? -5 : 5));
        event.preventDefault();
      }
    };
  }
  bindSeek(audio, byId('progress'));
  bindSeek(storeAudio, byId('store-progress'));
  audio.addEventListener('timeupdate', function () { finishSleepTimer(); updateProgress(); }, false);
  storeAudio.addEventListener('timeupdate', function () { finishSleepTimer(); updateStoreProgress(); }, false);
  audio.addEventListener('durationchange', updateProgress, false);
  storeAudio.addEventListener('durationchange', updateStoreProgress, false);
  audio.addEventListener('playing', function () {
    if (!finishSleepTimer()) { playing(true); message('player-status', ''); }
  }, false);
  storeAudio.addEventListener('playing', function () {
    if (!finishSleepTimer()) { message('store-status', ''); message('store-toggle', '暂停试听'); }
  }, false);
  ['pause', 'ended', 'emptied'].forEach(function (name) {
    storeAudio.addEventListener(name, function () { message('store-toggle', '播放试听'); }, false);
  });
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
