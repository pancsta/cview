package tcelllivepage

import "fmt"

// headTags returns the <style> and <script> tags injected into the HTML page.
func (s *Screen) headTags(fullPage bool) string {
	return "<style>" + s.screenCSS(fullPage) + "</style>" +
		"<script>" + screenJS + "</script>"
}

func (s *Screen) screenCSS(fullPage bool) string {
	fg := s.theme.Fg.CSS()
	bg := s.theme.Bg.CSS()
	cursor := s.theme.Cursor.CSS()

	css := ""
	if fullPage {
		// The default page is a full-page terminal; don't touch the global
		// layout of user-provided pages.
		css += "html,body{margin:0;padding:0;width:100%;height:100%;overflow:hidden;background:" + bg + ";}"
	}
	css += fmt.Sprintf(`.tcell-screen{position:relative;box-sizing:border-box;width:100%%;height:100%%;margin:0;padding:0;overflow:hidden;outline:none;background:%s;color:%s;font-family:%s;font-size:%dpx;line-height:1.2;white-space:pre;user-select:none;-webkit-user-select:none;tab-size:1;cursor:text}
.tcell-screen:focus,.tcell-screen:focus-visible{outline:none}
.tcell-grid{position:absolute;inset:0;white-space:pre}
.tcell-row{display:block;height:1.2em;line-height:1.2em;white-space:pre}
.tcell-probe{position:absolute;left:0;top:0;display:block;width:1ch;height:1.2em;visibility:hidden;pointer-events:none}
.tc-wide{display:inline-block;width:2ch;overflow:hidden;white-space:pre;vertical-align:baseline}
.tc-cursor{outline:1px solid %s;outline-offset:-1px}
@keyframes tc-blink{0%%,49%%{opacity:1}50%%,100%%{opacity:0}}
`, bg, fg, s.fontFamily, s.fontSize, cursor)

	return css
}

// screenJS forwards DOM input to the server and keeps the terminal focused. It
// uses the public livepage client API, so it works across HTML morphs.
const screenJS = `(function () {
  if (window.__tcellScreen) return;
  window.__tcellScreen = true;

  var SEL = '.tcell-screen';
  var MOVE_MS = 33;
  var lastMove = 0;
  var pressed = 0;
  var lastCell = null;
  var resizeTimer = 0;

  function screenEl() {
    return document.querySelector(SEL);
  }

  function send(type, payload) {
    if (!window.livepage) return false;
    var msg = {type: type};
    if (payload) msg.data = JSON.stringify(payload);
    window.livepage.send(msg);
    return true;
  }

  function measure() {
    var el = screenEl();
    if (!el) return null;
    var probe = el.querySelector('.tcell-probe');
    var cw = probe ? probe.getBoundingClientRect().width : 0;
    var ch = probe ? probe.getBoundingClientRect().height : 0;
    if (!(cw > 0)) cw = 8;
    if (!(ch > 0)) ch = 17;
    return {el: el, grid: el.querySelector('.tcell-grid'), cw: cw, ch: ch};
  }

  function cellAt(clientX, clientY, clamp) {
    var m = measure();
    if (!m || !m.grid) return null;
    var r = m.grid.getBoundingClientRect();
    var x = Math.floor((clientX - r.left) / m.cw);
    var y = Math.floor((clientY - r.top) / m.ch);
    var cols = parseInt(m.el.dataset.cols || '0', 10);
    var rows = parseInt(m.el.dataset.rows || '0', 10);
    if (!clamp && (x < 0 || y < 0 || (cols && x >= cols) || (rows && y >= rows))) {
      return null;
    }
    if (cols) x = Math.max(0, Math.min(cols - 1, x));
    if (rows) y = Math.max(0, Math.min(rows - 1, y));
    return {x: x, y: y};
  }

  function mods(e) {
    return {ctrl: !!e.ctrlKey, alt: !!e.altKey, shift: !!e.shiftKey, meta: !!e.metaKey};
  }

  function inside(t) {
    var el = screenEl();
    return !!(el && t instanceof Node && el.contains(t));
  }

  function focusScreen(force) {
    var el = screenEl();
    if (!el) return;
    if (!force && document.activeElement && document.activeElement !== document.body) return;
    try {
      el.focus({preventScroll: true});
    } catch (err) {
      el.focus();
    }
  }

  function buttonsMask(b) {
    var m = 0;
    if (b & 1) m |= 1; // left
    if (b & 2) m |= 2; // right
    if (b & 4) m |= 4; // middle
    return m;
  }

  function sendMove(e) {
    var now = Date.now();
    if (now - lastMove < MOVE_MS) return;
    lastMove = now;
    var p = cellAt(e.clientX, e.clientY, e.buttons !== 0);
    if (!p) return;
    lastCell = p;
    // The release happened outside the window.
    if (pressed && e.buttons === 0) {
      send('tcell-mouse', {action: 'up', x: p.x, y: p.y, button: 0, buttons: 0});
      pressed = 0;
    }
    var d = mods(e);
    d.action = 'move';
    d.x = p.x;
    d.y = p.y;
    d.buttons = buttonsMask(e.buttons);
    send('tcell-mouse', d);
  }

  function releaseAll() {
    if (!pressed) return;
    pressed = 0;
    var p = lastCell || {x: 0, y: 0};
    send('tcell-mouse', {action: 'up', x: p.x, y: p.y, button: 0, buttons: 0});
  }

  document.addEventListener('keydown', function (e) {
    var el = screenEl();
    if (!el || document.activeElement !== el) return;
    if (e.metaKey) return; // leave OS/browser shortcuts alone
    var d = mods(e);
    d.key = e.key;
    d.code = e.code;
    d.repeat = !!e.repeat;
    if (!send('tcell-key', d)) return;
    if (e.key !== 'F5' && e.key !== 'F11' && e.key !== 'F12') e.preventDefault();
  }, true);

  document.addEventListener('mousedown', function (e) {
    if (!inside(e.target)) return;
    focusScreen(true);
    pressed = e.buttons || 1;
    var p = cellAt(e.clientX, e.clientY, false);
    if (!p) return;
    lastCell = p;
    var d = mods(e);
    d.action = 'down';
    d.x = p.x;
    d.y = p.y;
    d.button = e.button;
    d.buttons = buttonsMask(e.buttons);
    send('tcell-mouse', d);
    e.preventDefault();
  }, true);

  document.addEventListener('mouseup', function (e) {
    var wasPressed = pressed;
    pressed = 0;
    if (!wasPressed && !inside(e.target)) return;
    var p = cellAt(e.clientX, e.clientY, true);
    if (!p) return;
    lastCell = p;
    var d = mods(e);
    d.action = 'up';
    d.x = p.x;
    d.y = p.y;
    d.button = e.button;
    d.buttons = buttonsMask(e.buttons);
    send('tcell-mouse', d);
  }, true);

  document.addEventListener('mousemove', function (e) {
    if (!inside(e.target) && !pressed) return;
    sendMove(e);
  }, true);

  document.addEventListener('wheel', function (e) {
    if (!inside(e.target)) return;
    var p = cellAt(e.clientX, e.clientY, false);
    if (!p) return;
    var d = mods(e);
    d.x = p.x;
    d.y = p.y;
    d.deltaX = e.deltaX;
    d.deltaY = e.deltaY;
    if (send('tcell-wheel', d)) e.preventDefault();
  }, {capture: true, passive: false});

  document.addEventListener('contextmenu', function (e) {
    if (inside(e.target)) e.preventDefault();
  }, true);

  function sendSize() {
    var m = measure();
    if (!m || !m.el) return;
    var cols = Math.max(1, Math.floor(m.el.clientWidth / m.cw));
    var rows = Math.max(1, Math.floor(m.el.clientHeight / m.ch));
    send('tcell-resize', {cols: cols, rows: rows});
  }

  function scheduleSize() {
    if (resizeTimer) clearTimeout(resizeTimer);
    resizeTimer = setTimeout(sendSize, 100);
  }

  window.addEventListener('resize', scheduleSize);
  window.addEventListener('blur', releaseAll);
  window.addEventListener('livepage:open', function () {
    focusScreen(false);
    sendSize();
  });
  window.addEventListener('livepage:morph', function () {
    focusScreen(false);
  });
  window.addEventListener('load', function () {
    focusScreen(false);
    if (document.fonts && document.fonts.ready) {
      document.fonts.ready.then(sendSize);
    }
  });
})();
`
