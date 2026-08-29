package agent

import (
	"strings"
)

// 存储垫片：从 api 包下沉到 agent 包，保证「预览注入的环境」与「浏览器实测的环境」完全一致。
// 沙箱 iframe 无 allow-same-origin 时产物访问 localStorage 会抛 SecurityError，
// 垫片在产物自身代码执行前探测并以内存存储降级。

// InjectStorageShim 在产物 HTML 中注入沙箱存储垫片。
// 只处理一次：已有垫片标记的文档直接原样返回。
func InjectStorageShim(html string) string {
	const marker = "<!--atomix-storage-shim-->"
	if strings.Contains(html, marker) {
		return html
	}
	headIdx := strings.Index(strings.ToLower(html), "<head")
	if headIdx < 0 {
		// 无 <head> 结构的产物，整体包裹一层保证垫片最先执行
		return marker + shimScript + html
	}
	// 插入点在 <head ...> 标签结束之后
	insertAt := strings.Index(html[headIdx:], ">")
	if insertAt < 0 {
		return marker + shimScript + html
	}
	insertAt = headIdx + insertAt + 1
	return html[:insertAt] + shimScript + html[insertAt:]
}

// shimScript 存储垫片脚本：在沙箱产物自身代码执行前运行。
// - 探测 localStorage 可用性（Chrome 内核下 opaque origin 直接访问即抛错）
// - 不可用时以内存 Map 实现完整 localStorage 接口语义，并对 window.sessionStorage 做同样保护
// - 历史数据可经 location.hash（#atomix-data=<urlencoded JSON>）恢复
// - 写入操作通过 postMessage 通知父页面，由父页面持久化
const shimScript = `
<!--atomix-storage-shim-->
<script>
(function () {
  var mem = {};
  function tryNative() {
    try {
      var w = window.localStorage;
      w.setItem('__atomix_probe__', '1');
      w.removeItem('__atomix_probe__');
      return w;
    } catch (e) { return null; }
  }
  function restoreFromHash(store) {
    try {
      var m = /(?:^|&)atomix-data=([^&]*)/.exec(location.hash.slice(1));
      if (m && m[1]) {
        var data = JSON.parse(decodeURIComponent(m[1]));
        for (var k in data) { store.setItem(k, data[k]); }
      }
    } catch (e) {}
  }
  function makeMemoryStore(native) {
    var nativeOK = !!native;
    function get(k) { return Object.prototype.hasOwnProperty.call(mem, k) ? mem[k] : null; }
    function set(k, v) {
      mem[k] = String(v);
      try { parent.postMessage({ source: 'atomix-shim', type: 'storage', key: k, value: mem[k] }, '*'); } catch (e) {}
    }
    function remove(k) {
      delete mem[k];
      if (nativeOK) { try { native.removeItem(k); } catch (e) {} }
      try { parent.postMessage({ source: 'atomix-shim', type: 'storage', key: k, value: null }, '*'); } catch (e) {}
    }
    var store = {
      getItem: get,
      setItem: set,
      removeItem: remove,
      clear: function () {
        mem = {};
        if (nativeOK) { try { native.clear(); } catch (e) {} }
        try { parent.postMessage({ source: 'atomix-shim', type: 'clear' }, '*'); } catch (e) {}
      },
      key: function (i) {
        var ks = Object.keys(mem);
        return i >= 0 && i < ks.length ? ks[i] : null;
      }
    };
    Object.defineProperty(store, 'length', { get: function () { return Object.keys(mem).length; } });
    restoreFromHash(store);
    return store;
  }
  var native = tryNative();
  if (native) {
    // 原生存储可用（如新窗口打开、或沙箱允许同源）：保留原生行为，仅尝试从 hash 恢复历史数据
    restoreFromHash(native);
  } else {
    var shim = makeMemoryStore(null);
    try {
      Object.defineProperty(window, 'localStorage', { value: shim, writable: true, configurable: true });
    } catch (e) {}
  }
  try {
    window.sessionStorage.getItem('__atomix_probe__');
  } catch (e) {
    var sMem = {};
    var sess = {
      getItem: function (k) { return Object.prototype.hasOwnProperty.call(sMem, k) ? sMem[k] : null; },
      setItem: function (k, v) { sMem[k] = String(v); },
      removeItem: function (k) { delete sMem[k]; },
      clear: function () { sMem = {}; },
      key: function (i) {
        var ks = Object.keys(sMem);
        return i >= 0 && i < ks.length ? ks[i] : null;
      }
    };
    Object.defineProperty(sess, 'length', { get: function () { return Object.keys(sMem).length; } });
    try {
      Object.defineProperty(window, 'sessionStorage', { value: sess, writable: true, configurable: true });
    } catch (e) {}
  }
  try { parent.postMessage({ source: 'atomix-shim', type: 'ready' }, '*'); } catch (e) {}
})();
</script>
`
