// node_modules/@anthropic-ai/claude-agent-sdk/sdk.mjs
import { createRequire as Oz } from "node:module";
import { execFile as Nre } from "child_process";
import { randomUUID as ow } from "crypto";
import { createReadStream as jre, realpathSync as Ure } from "fs";
import { copyFile as zre, mkdir as Qx, readdir as Lre, readFile as YU, rm as Fre, writeFile as QU } from "fs/promises";
import { createRequire as Bre } from "module";
import { homedir as ew, tmpdir as Hre } from "os";
import { dirname as WU, isAbsolute as ez, join as zt, relative as tz, resolve as fu, sep as iw } from "path";
import { createInterface as qre } from "readline";
import { fileURLToPath as Zre } from "url";
import { setMaxListeners as Mz } from "events";
import { realpathSync as Nz } from "fs";
import { dirname as yw, resolve as Pg, sep as bw } from "path";
import { spawn as gB } from "child_process";
import { existsSync as hB } from "fs";
import { createInterface as yB } from "readline";
import { homedir as n1 } from "os";
import { join as o1 } from "path";
import { randomUUID as wF } from "crypto";
import { join as lE } from "path";
import { AsyncLocalStorage as hF } from "async_hooks";
import { appendFile as yF, copyFile as bF, mkdir as _F, open as oE, readdir as iE, readFile as sE, stat as vF, unlink as SF, writeFile as hh } from "fs/promises";
import { randomBytes as lF } from "crypto";
import { chmod as uF, copyFile as dF, rename as pF, unlink as gh, writeFile as fF } from "fs/promises";
import { realpathSync as pT } from "fs";
import { cwd as w2 } from "process";
import { randomUUID as Ha } from "crypto";
import { appendFile as PT, mkdir as eB, rename as IT, stat as tB, symlink as rB, unlink as Rh } from "fs/promises";
import { dirname as $T, join as Mh, resolve as nB } from "path";
import * as Y from "fs";
import { lstat as j2, mkdir as U2, open as z2, readdir as L2, readFile as _T, rename as F2, rmdir as B2, rm as H2, stat as q2, unlink as Z2 } from "fs/promises";
import { existsSync as wB } from "fs";
import { readFile as v6 } from "fs/promises";
import { once as FR } from "events";
import { createWriteStream as n6 } from "fs";
import { open as BR, lstat as mye, readdir as Dy, realpath as o6, stat as i6 } from "fs/promises";
import { dirname as hye, join as Ya } from "path";
import { execFile as e6 } from "child_process";
import { promisify as t6 } from "util";
import { execFileSync as Zq } from "child_process";
import { lstatSync as Vq } from "fs";
import { join as Wq } from "path";
import { readdir as zy, stat as P6 } from "fs/promises";
import { basename as I6, join as Ly } from "path";
import { constants as e0 } from "fs";
import { open as D6, readdir as r0, rm as t0, stat as N6 } from "fs/promises";
import { join as ji } from "path";
import { join as By } from "path";
import { readdir as q6, readFile as d0 } from "fs/promises";
import { join as Hy } from "path";
import { createHash as tZ } from "crypto";
import { homedir as fbe, userInfo as rZ } from "os";
import { resolve as $re } from "path";
import { join as Ns } from "path";
import { dirname as LQ } from "path";
import Ne from "node:path";
import UN from "node:os";
import $x from "node:process";
import { join as uee } from "path";
import { readdir as rRe, readFile as lee } from "fs/promises";
import { release as KN } from "os";
import { dirname as lre, join as Xn, resolve as uu } from "path";
import { join as qee } from "path";
import { userInfo as Gee } from "os";
import { isAbsolute as Rj } from "path";
import { execFile as wre } from "child_process";
import { existsSync as kre } from "fs";
var xz = Object.create;
var { getPrototypeOf: wz, defineProperty: Eg, getOwnPropertyNames: kz } = Object;
var Ez = Object.prototype.hasOwnProperty;
function Tz(e) {
  return this[e];
}
var Pz;
var Iz;
var Tg = (e, t, r) => {
  var o = e != null && typeof e === "object";
  if (o) {
    var n = t ? Pz ??= /* @__PURE__ */ new WeakMap() : Iz ??= /* @__PURE__ */ new WeakMap(), i = n.get(e);
    if (i) return i;
  }
  r = e != null ? xz(wz(e)) : {};
  let s = t || !e || !e.__esModule ? Eg(r, "default", { value: e, enumerable: true }) : r;
  for (let a of kz(e)) if (!Ez.call(s, a)) Eg(s, a, { get: Tz.bind(e, a), enumerable: true });
  if (o) n.set(e, s);
  return s;
};
var k = (e, t) => () => (t || e((t = { exports: {} }).exports, t), t.exports);
var Rz = (e) => e;
function $z(e, t) {
  this[e] = Rz.bind(null, t);
}
var xr = (e, t) => {
  for (var r in t) Eg(e, r, { get: t[r], enumerable: true, configurable: true, set: $z.bind(t, r) });
};
var Ot = Oz(import.meta.url);
var Az = Symbol.dispose || Symbol.for("Symbol.dispose");
var Cz = Symbol.asyncDispose || Symbol.for("Symbol.asyncDispose");
var _e = (e, t, r) => {
  if (t != null) {
    if (typeof t !== "object" && typeof t !== "function") throw TypeError('Object expected to be assigned to "using" declaration');
    var o;
    if (r) o = t[Cz];
    if (o === void 0) o = t[Az];
    if (typeof o !== "function") throw TypeError("Object not disposable");
    e.push([r, o, t]);
  } else if (r) e.push([r]);
  return t;
};
var ve = (e, t, r) => {
  var o = typeof SuppressedError === "function" ? SuppressedError : function(s, a, c, u) {
    return u = Error(c), u.name = "SuppressedError", u.error = s, u.suppressed = a, u;
  }, n = (s) => t = r ? new o(s, t, "An error was suppressed during disposal") : (r = true, s), i = (s) => {
    while (s = e.pop()) try {
      var a = s[1] && s[1].call(s[2]);
      if (s[0]) return Promise.resolve(a).then(i, (c) => (n(c), i()));
    } catch (c) {
      n(c);
    }
    if (r) throw t;
  };
  return i();
};
var ZT = k((HT) => {
  Object.defineProperty(HT, "__esModule", { value: true });
  HT._globalThis = void 0;
  HT._globalThis = typeof globalThis === "object" ? globalThis : global;
});
var VT = k((go) => {
  var IB = go && go.__createBinding || (Object.create ? function(e, t, r, o) {
    if (o === void 0) o = r;
    Object.defineProperty(e, o, { enumerable: true, get: function() {
      return t[r];
    } });
  } : function(e, t, r, o) {
    if (o === void 0) o = r;
    e[o] = t[r];
  }), RB = go && go.__exportStar || function(e, t) {
    for (var r in e) if (r !== "default" && !Object.prototype.hasOwnProperty.call(t, r)) IB(t, e, r);
  };
  Object.defineProperty(go, "__esModule", { value: true });
  RB(ZT(), go);
});
var WT = k((ho) => {
  var $B = ho && ho.__createBinding || (Object.create ? function(e, t, r, o) {
    if (o === void 0) o = r;
    Object.defineProperty(e, o, { enumerable: true, get: function() {
      return t[r];
    } });
  } : function(e, t, r, o) {
    if (o === void 0) o = r;
    e[o] = t[r];
  }), OB = ho && ho.__exportStar || function(e, t) {
    for (var r in e) if (r !== "default" && !Object.prototype.hasOwnProperty.call(t, r)) $B(t, e, r);
  };
  Object.defineProperty(ho, "__esModule", { value: true });
  OB(VT(), ho);
});
var Fh = k((KT) => {
  Object.defineProperty(KT, "__esModule", { value: true });
  KT.VERSION = void 0;
  KT.VERSION = "1.9.0";
});
var eP = k((YT) => {
  Object.defineProperty(YT, "__esModule", { value: true });
  YT.isCompatible = YT._makeCompatibilityCheck = void 0;
  var AB = Fh(), JT = /^(\d+)\.(\d+)\.(\d+)(-(.+))?$/;
  function XT(e) {
    let t = /* @__PURE__ */ new Set([e]), r = /* @__PURE__ */ new Set(), o = e.match(JT);
    if (!o) return () => false;
    let n = { major: +o[1], minor: +o[2], patch: +o[3], prerelease: o[4] };
    if (n.prerelease != null) return function(c) {
      return c === e;
    };
    function i(a) {
      return r.add(a), false;
    }
    function s(a) {
      return t.add(a), true;
    }
    return function(c) {
      if (t.has(c)) return true;
      if (r.has(c)) return false;
      let u = c.match(JT);
      if (!u) return i(c);
      let d = { major: +u[1], minor: +u[2], patch: +u[3], prerelease: u[4] };
      if (d.prerelease != null) return i(c);
      if (n.major !== d.major) return i(c);
      if (n.major === 0) {
        if (n.minor === d.minor && n.patch <= d.patch) return s(c);
        return i(c);
      }
      if (n.minor <= d.minor) return s(c);
      return i(c);
    };
  }
  YT._makeCompatibilityCheck = XT;
  YT.isCompatible = XT(AB.VERSION);
});
var yo = k((tP) => {
  Object.defineProperty(tP, "__esModule", { value: true });
  tP.unregisterGlobal = tP.getGlobal = tP.registerGlobal = void 0;
  var MB = WT(), $i = Fh(), DB = eP(), NB = $i.VERSION.split(".")[0], Va = Symbol.for(`opentelemetry.js.api.${NB}`), Wa = MB._globalThis;
  function jB(e, t, r, o = false) {
    var n;
    let i = Wa[Va] = (n = Wa[Va]) !== null && n !== void 0 ? n : { version: $i.VERSION };
    if (!o && i[e]) {
      let s = Error(`@opentelemetry/api: Attempted duplicate registration of API: ${e}`);
      return r.error(s.stack || s.message), false;
    }
    if (i.version !== $i.VERSION) {
      let s = Error(`@opentelemetry/api: Registration of version v${i.version} for ${e} does not match previously registered API v${$i.VERSION}`);
      return r.error(s.stack || s.message), false;
    }
    return i[e] = t, r.debug(`@opentelemetry/api: Registered a global for ${e} v${$i.VERSION}.`), true;
  }
  tP.registerGlobal = jB;
  function UB(e) {
    var t, r;
    let o = (t = Wa[Va]) === null || t === void 0 ? void 0 : t.version;
    if (!o || !(0, DB.isCompatible)(o)) return;
    return (r = Wa[Va]) === null || r === void 0 ? void 0 : r[e];
  }
  tP.getGlobal = UB;
  function zB(e, t) {
    t.debug(`@opentelemetry/api: Unregistering a global for ${e} v${$i.VERSION}.`);
    let r = Wa[Va];
    if (r) delete r[e];
  }
  tP.unregisterGlobal = zB;
});
var sP = k((oP) => {
  Object.defineProperty(oP, "__esModule", { value: true });
  oP.DiagComponentLogger = void 0;
  var BB = yo();
  class nP {
    constructor(e) {
      this._namespace = e.namespace || "DiagComponentLogger";
    }
    debug(...e) {
      return Ka("debug", this._namespace, e);
    }
    error(...e) {
      return Ka("error", this._namespace, e);
    }
    info(...e) {
      return Ka("info", this._namespace, e);
    }
    warn(...e) {
      return Ka("warn", this._namespace, e);
    }
    verbose(...e) {
      return Ka("verbose", this._namespace, e);
    }
  }
  oP.DiagComponentLogger = nP;
  function Ka(e, t, r) {
    let o = (0, BB.getGlobal)("diag");
    if (!o) return;
    return r.unshift(t), o[e](...r);
  }
});
var xd = k((aP) => {
  Object.defineProperty(aP, "__esModule", { value: true });
  aP.DiagLogLevel = void 0;
  var HB;
  (function(e) {
    e[e.NONE = 0] = "NONE", e[e.ERROR = 30] = "ERROR", e[e.WARN = 50] = "WARN", e[e.INFO = 60] = "INFO", e[e.DEBUG = 70] = "DEBUG", e[e.VERBOSE = 80] = "VERBOSE", e[e.ALL = 9999] = "ALL";
  })(HB = aP.DiagLogLevel || (aP.DiagLogLevel = {}));
});
var uP = k((cP) => {
  Object.defineProperty(cP, "__esModule", { value: true });
  cP.createLogLevelDiagLogger = void 0;
  var Fr = xd();
  function qB(e, t) {
    if (e < Fr.DiagLogLevel.NONE) e = Fr.DiagLogLevel.NONE;
    else if (e > Fr.DiagLogLevel.ALL) e = Fr.DiagLogLevel.ALL;
    t = t || {};
    function r(o, n) {
      let i = t[o];
      if (typeof i === "function" && e >= n) return i.bind(t);
      return function() {
      };
    }
    return { error: r("error", Fr.DiagLogLevel.ERROR), warn: r("warn", Fr.DiagLogLevel.WARN), info: r("info", Fr.DiagLogLevel.INFO), debug: r("debug", Fr.DiagLogLevel.DEBUG), verbose: r("verbose", Fr.DiagLogLevel.VERBOSE) };
  }
  cP.createLogLevelDiagLogger = qB;
});
var bo = k((pP) => {
  Object.defineProperty(pP, "__esModule", { value: true });
  pP.DiagAPI = void 0;
  var ZB = sP(), VB = uP(), dP = xd(), wd = yo(), WB = "diag";
  class Hh {
    constructor() {
      function e(o) {
        return function(...n) {
          let i = (0, wd.getGlobal)("diag");
          if (!i) return;
          return i[o](...n);
        };
      }
      let t = this, r = (o, n = { logLevel: dP.DiagLogLevel.INFO }) => {
        var i, s, a;
        if (o === t) {
          let d = Error("Cannot use diag as the logger for itself. Please use a DiagLogger implementation like ConsoleDiagLogger or a custom implementation");
          return t.error((i = d.stack) !== null && i !== void 0 ? i : d.message), false;
        }
        if (typeof n === "number") n = { logLevel: n };
        let c = (0, wd.getGlobal)("diag"), u = (0, VB.createLogLevelDiagLogger)((s = n.logLevel) !== null && s !== void 0 ? s : dP.DiagLogLevel.INFO, o);
        if (c && !n.suppressOverrideMessage) {
          let d = (a = Error().stack) !== null && a !== void 0 ? a : "<failed to generate stacktrace>";
          c.warn(`Current logger will be overwritten from ${d}`), u.warn(`Current logger will overwrite one already registered from ${d}`);
        }
        return (0, wd.registerGlobal)("diag", u, t, true);
      };
      t.setLogger = r, t.disable = () => {
        (0, wd.unregisterGlobal)(WB, t);
      }, t.createComponentLogger = (o) => new ZB.DiagComponentLogger(o), t.verbose = e("verbose"), t.debug = e("debug"), t.info = e("info"), t.warn = e("warn"), t.error = e("error");
    }
    static instance() {
      if (!this._instance) this._instance = new Hh();
      return this._instance;
    }
  }
  pP.DiagAPI = Hh;
});
var hP = k((mP) => {
  Object.defineProperty(mP, "__esModule", { value: true });
  mP.BaggageImpl = void 0;
  class Oi {
    constructor(e) {
      this._entries = e ? new Map(e) : /* @__PURE__ */ new Map();
    }
    getEntry(e) {
      let t = this._entries.get(e);
      if (!t) return;
      return Object.assign({}, t);
    }
    getAllEntries() {
      return Array.from(this._entries.entries()).map(([e, t]) => [e, t]);
    }
    setEntry(e, t) {
      let r = new Oi(this._entries);
      return r._entries.set(e, t), r;
    }
    removeEntry(e) {
      let t = new Oi(this._entries);
      return t._entries.delete(e), t;
    }
    removeEntries(...e) {
      let t = new Oi(this._entries);
      for (let r of e) t._entries.delete(r);
      return t;
    }
    clear() {
      return new Oi();
    }
  }
  mP.BaggageImpl = Oi;
});
var _P = k((yP) => {
  Object.defineProperty(yP, "__esModule", { value: true });
  yP.baggageEntryMetadataSymbol = void 0;
  yP.baggageEntryMetadataSymbol = Symbol("BaggageEntryMetadata");
});
var qh = k((vP) => {
  Object.defineProperty(vP, "__esModule", { value: true });
  vP.baggageEntryMetadataFromString = vP.createBaggage = void 0;
  var KB = bo(), GB = hP(), JB = _P(), XB = KB.DiagAPI.instance();
  function YB(e = {}) {
    return new GB.BaggageImpl(new Map(Object.entries(e)));
  }
  vP.createBaggage = YB;
  function QB(e) {
    if (typeof e !== "string") XB.error(`Cannot create baggage metadata from unknown type: ${typeof e}`), e = "";
    return { __TYPE__: JB.baggageEntryMetadataSymbol, toString() {
      return e;
    } };
  }
  vP.baggageEntryMetadataFromString = QB;
});
var Ga = k((xP) => {
  Object.defineProperty(xP, "__esModule", { value: true });
  xP.ROOT_CONTEXT = xP.createContextKey = void 0;
  function tH(e) {
    return Symbol.for(e);
  }
  xP.createContextKey = tH;
  class kd {
    constructor(e) {
      let t = this;
      t._currentContext = e ? new Map(e) : /* @__PURE__ */ new Map(), t.getValue = (r) => t._currentContext.get(r), t.setValue = (r, o) => {
        let n = new kd(t._currentContext);
        return n._currentContext.set(r, o), n;
      }, t.deleteValue = (r) => {
        let o = new kd(t._currentContext);
        return o._currentContext.delete(r), o;
      };
    }
  }
  xP.ROOT_CONTEXT = new kd();
});
var PP = k((EP) => {
  Object.defineProperty(EP, "__esModule", { value: true });
  EP.DiagConsoleLogger = void 0;
  var Zh = [{ n: "error", c: "error" }, { n: "warn", c: "warn" }, { n: "info", c: "info" }, { n: "debug", c: "debug" }, { n: "verbose", c: "trace" }];
  class kP {
    constructor() {
      function e(t) {
        return function(...r) {
          if (console) {
            let o = console[t];
            if (typeof o !== "function") o = console.log;
            if (typeof o === "function") return o.apply(console, r);
          }
        };
      }
      for (let t = 0; t < Zh.length; t++) this[Zh[t].n] = e(Zh[t].c);
    }
  }
  EP.DiagConsoleLogger = kP;
});
var ey = k((IP) => {
  Object.defineProperty(IP, "__esModule", { value: true });
  IP.createNoopMeter = IP.NOOP_OBSERVABLE_UP_DOWN_COUNTER_METRIC = IP.NOOP_OBSERVABLE_GAUGE_METRIC = IP.NOOP_OBSERVABLE_COUNTER_METRIC = IP.NOOP_UP_DOWN_COUNTER_METRIC = IP.NOOP_HISTOGRAM_METRIC = IP.NOOP_GAUGE_METRIC = IP.NOOP_COUNTER_METRIC = IP.NOOP_METER = IP.NoopObservableUpDownCounterMetric = IP.NoopObservableGaugeMetric = IP.NoopObservableCounterMetric = IP.NoopObservableMetric = IP.NoopHistogramMetric = IP.NoopGaugeMetric = IP.NoopUpDownCounterMetric = IP.NoopCounterMetric = IP.NoopMetric = IP.NoopMeter = void 0;
  class Vh {
    constructor() {
    }
    createGauge(e, t) {
      return IP.NOOP_GAUGE_METRIC;
    }
    createHistogram(e, t) {
      return IP.NOOP_HISTOGRAM_METRIC;
    }
    createCounter(e, t) {
      return IP.NOOP_COUNTER_METRIC;
    }
    createUpDownCounter(e, t) {
      return IP.NOOP_UP_DOWN_COUNTER_METRIC;
    }
    createObservableGauge(e, t) {
      return IP.NOOP_OBSERVABLE_GAUGE_METRIC;
    }
    createObservableCounter(e, t) {
      return IP.NOOP_OBSERVABLE_COUNTER_METRIC;
    }
    createObservableUpDownCounter(e, t) {
      return IP.NOOP_OBSERVABLE_UP_DOWN_COUNTER_METRIC;
    }
    addBatchObservableCallback(e, t) {
    }
    removeBatchObservableCallback(e) {
    }
  }
  IP.NoopMeter = Vh;
  class Ai {
  }
  IP.NoopMetric = Ai;
  class Wh extends Ai {
    add(e, t) {
    }
  }
  IP.NoopCounterMetric = Wh;
  class Kh extends Ai {
    add(e, t) {
    }
  }
  IP.NoopUpDownCounterMetric = Kh;
  class Gh extends Ai {
    record(e, t) {
    }
  }
  IP.NoopGaugeMetric = Gh;
  class Jh extends Ai {
    record(e, t) {
    }
  }
  IP.NoopHistogramMetric = Jh;
  class Ja {
    addCallback(e) {
    }
    removeCallback(e) {
    }
  }
  IP.NoopObservableMetric = Ja;
  class Xh extends Ja {
  }
  IP.NoopObservableCounterMetric = Xh;
  class Yh extends Ja {
  }
  IP.NoopObservableGaugeMetric = Yh;
  class Qh extends Ja {
  }
  IP.NoopObservableUpDownCounterMetric = Qh;
  IP.NOOP_METER = new Vh();
  IP.NOOP_COUNTER_METRIC = new Wh();
  IP.NOOP_GAUGE_METRIC = new Gh();
  IP.NOOP_HISTOGRAM_METRIC = new Jh();
  IP.NOOP_UP_DOWN_COUNTER_METRIC = new Kh();
  IP.NOOP_OBSERVABLE_COUNTER_METRIC = new Xh();
  IP.NOOP_OBSERVABLE_GAUGE_METRIC = new Yh();
  IP.NOOP_OBSERVABLE_UP_DOWN_COUNTER_METRIC = new Qh();
  function nH() {
    return IP.NOOP_METER;
  }
  IP.createNoopMeter = nH;
});
var zP = k((UP) => {
  Object.defineProperty(UP, "__esModule", { value: true });
  UP.ValueType = void 0;
  var mH;
  (function(e) {
    e[e.INT = 0] = "INT", e[e.DOUBLE = 1] = "DOUBLE";
  })(mH = UP.ValueType || (UP.ValueType = {}));
});
var ry = k((LP) => {
  Object.defineProperty(LP, "__esModule", { value: true });
  LP.defaultTextMapSetter = LP.defaultTextMapGetter = void 0;
  LP.defaultTextMapGetter = { get(e, t) {
    if (e == null) return;
    return e[t];
  }, keys(e) {
    if (e == null) return [];
    return Object.keys(e);
  } };
  LP.defaultTextMapSetter = { set(e, t, r) {
    if (e == null) return;
    e[t] = r;
  } };
});
var ZP = k((HP) => {
  Object.defineProperty(HP, "__esModule", { value: true });
  HP.NoopContextManager = void 0;
  var hH = Ga();
  class BP {
    active() {
      return hH.ROOT_CONTEXT;
    }
    with(e, t, r, ...o) {
      return t.call(r, ...o);
    }
    bind(e, t) {
      return t;
    }
    enable() {
      return this;
    }
    disable() {
      return this;
    }
  }
  HP.NoopContextManager = BP;
});
var Xa = k((WP) => {
  Object.defineProperty(WP, "__esModule", { value: true });
  WP.ContextAPI = void 0;
  var yH = ZP(), ny = yo(), VP = bo(), oy = "context", bH = new yH.NoopContextManager();
  class iy {
    constructor() {
    }
    static getInstance() {
      if (!this._instance) this._instance = new iy();
      return this._instance;
    }
    setGlobalContextManager(e) {
      return (0, ny.registerGlobal)(oy, e, VP.DiagAPI.instance());
    }
    active() {
      return this._getContextManager().active();
    }
    with(e, t, r, ...o) {
      return this._getContextManager().with(e, t, r, ...o);
    }
    bind(e, t) {
      return this._getContextManager().bind(e, t);
    }
    _getContextManager() {
      return (0, ny.getGlobal)(oy) || bH;
    }
    disable() {
      this._getContextManager().disable(), (0, ny.unregisterGlobal)(oy, VP.DiagAPI.instance());
    }
  }
  WP.ContextAPI = iy;
});
var ay = k((GP) => {
  Object.defineProperty(GP, "__esModule", { value: true });
  GP.TraceFlags = void 0;
  var _H;
  (function(e) {
    e[e.NONE = 0] = "NONE", e[e.SAMPLED = 1] = "SAMPLED";
  })(_H = GP.TraceFlags || (GP.TraceFlags = {}));
});
var Ed = k((JP) => {
  Object.defineProperty(JP, "__esModule", { value: true });
  JP.INVALID_SPAN_CONTEXT = JP.INVALID_TRACEID = JP.INVALID_SPANID = void 0;
  var vH = ay();
  JP.INVALID_SPANID = "0000000000000000";
  JP.INVALID_TRACEID = "00000000000000000000000000000000";
  JP.INVALID_SPAN_CONTEXT = { traceId: JP.INVALID_TRACEID, spanId: JP.INVALID_SPANID, traceFlags: vH.TraceFlags.NONE };
});
var Td = k((tI) => {
  Object.defineProperty(tI, "__esModule", { value: true });
  tI.NonRecordingSpan = void 0;
  var SH = Ed();
  class eI {
    constructor(e = SH.INVALID_SPAN_CONTEXT) {
      this._spanContext = e;
    }
    spanContext() {
      return this._spanContext;
    }
    setAttribute(e, t) {
      return this;
    }
    setAttributes(e) {
      return this;
    }
    addEvent(e, t) {
      return this;
    }
    addLink(e) {
      return this;
    }
    addLinks(e) {
      return this;
    }
    setStatus(e) {
      return this;
    }
    updateName(e) {
      return this;
    }
    end(e) {
    }
    isRecording() {
      return false;
    }
    recordException(e, t) {
    }
  }
  tI.NonRecordingSpan = eI;
});
var uy = k((oI) => {
  Object.defineProperty(oI, "__esModule", { value: true });
  oI.getSpanContext = oI.setSpanContext = oI.deleteSpan = oI.setSpan = oI.getActiveSpan = oI.getSpan = void 0;
  var xH = Ga(), wH = Td(), kH = Xa(), cy = (0, xH.createContextKey)("OpenTelemetry Context Key SPAN");
  function ly(e) {
    return e.getValue(cy) || void 0;
  }
  oI.getSpan = ly;
  function EH() {
    return ly(kH.ContextAPI.getInstance().active());
  }
  oI.getActiveSpan = EH;
  function nI(e, t) {
    return e.setValue(cy, t);
  }
  oI.setSpan = nI;
  function TH(e) {
    return e.deleteValue(cy);
  }
  oI.deleteSpan = TH;
  function PH(e, t) {
    return nI(e, new wH.NonRecordingSpan(t));
  }
  oI.setSpanContext = PH;
  function IH(e) {
    var t;
    return (t = ly(e)) === null || t === void 0 ? void 0 : t.spanContext();
  }
  oI.getSpanContext = IH;
});
var Pd = k((lI) => {
  Object.defineProperty(lI, "__esModule", { value: true });
  lI.wrapSpanContext = lI.isSpanContextValid = lI.isValidSpanId = lI.isValidTraceId = void 0;
  var sI = Ed(), MH = Td(), DH = /^([0-9a-f]{32})$/i, NH = /^[0-9a-f]{16}$/i;
  function aI(e) {
    return DH.test(e) && e !== sI.INVALID_TRACEID;
  }
  lI.isValidTraceId = aI;
  function cI(e) {
    return NH.test(e) && e !== sI.INVALID_SPANID;
  }
  lI.isValidSpanId = cI;
  function jH(e) {
    return aI(e.traceId) && cI(e.spanId);
  }
  lI.isSpanContextValid = jH;
  function UH(e) {
    return new MH.NonRecordingSpan(e);
  }
  lI.wrapSpanContext = UH;
});
var fy = k((fI) => {
  Object.defineProperty(fI, "__esModule", { value: true });
  fI.NoopTracer = void 0;
  var BH = Xa(), dI = uy(), dy = Td(), HH = Pd(), py = BH.ContextAPI.getInstance();
  class pI {
    startSpan(e, t, r = py.active()) {
      if (Boolean(t === null || t === void 0 ? void 0 : t.root)) return new dy.NonRecordingSpan();
      let n = r && (0, dI.getSpanContext)(r);
      if (qH(n) && (0, HH.isSpanContextValid)(n)) return new dy.NonRecordingSpan(n);
      else return new dy.NonRecordingSpan();
    }
    startActiveSpan(e, t, r, o) {
      let n, i, s;
      if (arguments.length < 2) return;
      else if (arguments.length === 2) s = t;
      else if (arguments.length === 3) n = t, s = r;
      else n = t, i = r, s = o;
      let a = i !== null && i !== void 0 ? i : py.active(), c = this.startSpan(e, n, a), u = (0, dI.setSpan)(a, c);
      return py.with(u, s, void 0, c);
    }
  }
  fI.NoopTracer = pI;
  function qH(e) {
    return typeof e === "object" && typeof e.spanId === "string" && typeof e.traceId === "string" && typeof e.traceFlags === "number";
  }
});
var my = k((hI) => {
  Object.defineProperty(hI, "__esModule", { value: true });
  hI.ProxyTracer = void 0;
  var ZH = fy(), VH = new ZH.NoopTracer();
  class gI {
    constructor(e, t, r, o) {
      this._provider = e, this.name = t, this.version = r, this.options = o;
    }
    startSpan(e, t, r) {
      return this._getTracer().startSpan(e, t, r);
    }
    startActiveSpan(e, t, r, o) {
      let n = this._getTracer();
      return Reflect.apply(n.startActiveSpan, n, arguments);
    }
    _getTracer() {
      if (this._delegate) return this._delegate;
      let e = this._provider.getDelegateTracer(this.name, this.version, this.options);
      if (!e) return VH;
      return this._delegate = e, this._delegate;
    }
  }
  hI.ProxyTracer = gI;
});
var SI = k((_I) => {
  Object.defineProperty(_I, "__esModule", { value: true });
  _I.NoopTracerProvider = void 0;
  var WH = fy();
  class bI {
    getTracer(e, t, r) {
      return new WH.NoopTracer();
    }
  }
  _I.NoopTracerProvider = bI;
});
var gy = k((wI) => {
  Object.defineProperty(wI, "__esModule", { value: true });
  wI.ProxyTracerProvider = void 0;
  var KH = my(), GH = SI(), JH = new GH.NoopTracerProvider();
  class xI {
    getTracer(e, t, r) {
      var o;
      return (o = this.getDelegateTracer(e, t, r)) !== null && o !== void 0 ? o : new KH.ProxyTracer(this, e, t, r);
    }
    getDelegate() {
      var e;
      return (e = this._delegate) !== null && e !== void 0 ? e : JH;
    }
    setDelegate(e) {
      this._delegate = e;
    }
    getDelegateTracer(e, t, r) {
      var o;
      return (o = this._delegate) === null || o === void 0 ? void 0 : o.getTracer(e, t, r);
    }
  }
  wI.ProxyTracerProvider = xI;
});
var TI = k((EI) => {
  Object.defineProperty(EI, "__esModule", { value: true });
  EI.SamplingDecision = void 0;
  var XH;
  (function(e) {
    e[e.NOT_RECORD = 0] = "NOT_RECORD", e[e.RECORD = 1] = "RECORD", e[e.RECORD_AND_SAMPLED = 2] = "RECORD_AND_SAMPLED";
  })(XH = EI.SamplingDecision || (EI.SamplingDecision = {}));
});
var II = k((PI) => {
  Object.defineProperty(PI, "__esModule", { value: true });
  PI.SpanKind = void 0;
  var YH;
  (function(e) {
    e[e.INTERNAL = 0] = "INTERNAL", e[e.SERVER = 1] = "SERVER", e[e.CLIENT = 2] = "CLIENT", e[e.PRODUCER = 3] = "PRODUCER", e[e.CONSUMER = 4] = "CONSUMER";
  })(YH = PI.SpanKind || (PI.SpanKind = {}));
});
var $I = k((RI) => {
  Object.defineProperty(RI, "__esModule", { value: true });
  RI.SpanStatusCode = void 0;
  var QH;
  (function(e) {
    e[e.UNSET = 0] = "UNSET", e[e.OK = 1] = "OK", e[e.ERROR = 2] = "ERROR";
  })(QH = RI.SpanStatusCode || (RI.SpanStatusCode = {}));
});
var CI = k((OI) => {
  Object.defineProperty(OI, "__esModule", { value: true });
  OI.validateValue = OI.validateKey = void 0;
  var _y = "[_0-9a-z-*/]", eq = `[a-z]${_y}{0,255}`, tq = `[a-z0-9]${_y}{0,240}@[a-z]${_y}{0,13}`, rq = new RegExp(`^(?:${eq}|${tq})$`), nq = /^[ -~]{0,255}[!-~]$/, oq = /,|=/;
  function iq(e) {
    return rq.test(e);
  }
  OI.validateKey = iq;
  function sq(e) {
    return nq.test(e) && !oq.test(e);
  }
  OI.validateValue = sq;
});
var LI = k((UI) => {
  Object.defineProperty(UI, "__esModule", { value: true });
  UI.TraceStateImpl = void 0;
  var MI = CI(), DI = 32, cq = 512, NI = ",", jI = "=";
  class vy {
    constructor(e) {
      if (this._internalState = /* @__PURE__ */ new Map(), e) this._parse(e);
    }
    set(e, t) {
      let r = this._clone();
      if (r._internalState.has(e)) r._internalState.delete(e);
      return r._internalState.set(e, t), r;
    }
    unset(e) {
      let t = this._clone();
      return t._internalState.delete(e), t;
    }
    get(e) {
      return this._internalState.get(e);
    }
    serialize() {
      return this._keys().reduce((e, t) => (e.push(t + jI + this.get(t)), e), []).join(NI);
    }
    _parse(e) {
      if (e.length > cq) return;
      if (this._internalState = e.split(NI).reverse().reduce((t, r) => {
        let o = r.trim(), n = o.indexOf(jI);
        if (n !== -1) {
          let i = o.slice(0, n), s = o.slice(n + 1, r.length);
          if ((0, MI.validateKey)(i) && (0, MI.validateValue)(s)) t.set(i, s);
        }
        return t;
      }, /* @__PURE__ */ new Map()), this._internalState.size > DI) this._internalState = new Map(Array.from(this._internalState.entries()).reverse().slice(0, DI));
    }
    _keys() {
      return Array.from(this._internalState.keys()).reverse();
    }
    _clone() {
      let e = new vy();
      return e._internalState = new Map(this._internalState), e;
    }
  }
  UI.TraceStateImpl = vy;
});
var HI = k((FI) => {
  Object.defineProperty(FI, "__esModule", { value: true });
  FI.createTraceState = void 0;
  var lq = LI();
  function uq(e) {
    return new lq.TraceStateImpl(e);
  }
  FI.createTraceState = uq;
});
var VI = k((qI) => {
  Object.defineProperty(qI, "__esModule", { value: true });
  qI.context = void 0;
  var dq = Xa();
  qI.context = dq.ContextAPI.getInstance();
});
var GI = k((WI) => {
  Object.defineProperty(WI, "__esModule", { value: true });
  WI.diag = void 0;
  var pq = bo();
  WI.diag = pq.DiagAPI.instance();
});
var YI = k((JI) => {
  Object.defineProperty(JI, "__esModule", { value: true });
  JI.NOOP_METER_PROVIDER = JI.NoopMeterProvider = void 0;
  var fq = ey();
  class Sy {
    getMeter(e, t, r) {
      return fq.NOOP_METER;
    }
  }
  JI.NoopMeterProvider = Sy;
  JI.NOOP_METER_PROVIDER = new Sy();
});
var rR = k((eR) => {
  Object.defineProperty(eR, "__esModule", { value: true });
  eR.MetricsAPI = void 0;
  var gq = YI(), xy = yo(), QI = bo(), wy = "metrics";
  class ky {
    constructor() {
    }
    static getInstance() {
      if (!this._instance) this._instance = new ky();
      return this._instance;
    }
    setGlobalMeterProvider(e) {
      return (0, xy.registerGlobal)(wy, e, QI.DiagAPI.instance());
    }
    getMeterProvider() {
      return (0, xy.getGlobal)(wy) || gq.NOOP_METER_PROVIDER;
    }
    getMeter(e, t, r) {
      return this.getMeterProvider().getMeter(e, t, r);
    }
    disable() {
      (0, xy.unregisterGlobal)(wy, QI.DiagAPI.instance());
    }
  }
  eR.MetricsAPI = ky;
});
var iR = k((nR) => {
  Object.defineProperty(nR, "__esModule", { value: true });
  nR.metrics = void 0;
  var hq = rR();
  nR.metrics = hq.MetricsAPI.getInstance();
});
var lR = k((aR) => {
  Object.defineProperty(aR, "__esModule", { value: true });
  aR.NoopTextMapPropagator = void 0;
  class sR {
    inject(e, t) {
    }
    extract(e, t) {
      return e;
    }
    fields() {
      return [];
    }
  }
  aR.NoopTextMapPropagator = sR;
});
var fR = k((dR) => {
  Object.defineProperty(dR, "__esModule", { value: true });
  dR.deleteBaggage = dR.setBaggage = dR.getActiveBaggage = dR.getBaggage = void 0;
  var yq = Xa(), bq = Ga(), Ey = (0, bq.createContextKey)("OpenTelemetry Baggage Key");
  function uR(e) {
    return e.getValue(Ey) || void 0;
  }
  dR.getBaggage = uR;
  function _q() {
    return uR(yq.ContextAPI.getInstance().active());
  }
  dR.getActiveBaggage = _q;
  function vq(e, t) {
    return e.setValue(Ey, t);
  }
  dR.setBaggage = vq;
  function Sq(e) {
    return e.deleteValue(Ey);
  }
  dR.deleteBaggage = Sq;
});
var bR = k((hR) => {
  Object.defineProperty(hR, "__esModule", { value: true });
  hR.PropagationAPI = void 0;
  var Ty = yo(), Eq = lR(), mR = ry(), Id = fR(), Tq = qh(), gR = bo(), Py = "propagation", Pq = new Eq.NoopTextMapPropagator();
  class Iy {
    constructor() {
      this.createBaggage = Tq.createBaggage, this.getBaggage = Id.getBaggage, this.getActiveBaggage = Id.getActiveBaggage, this.setBaggage = Id.setBaggage, this.deleteBaggage = Id.deleteBaggage;
    }
    static getInstance() {
      if (!this._instance) this._instance = new Iy();
      return this._instance;
    }
    setGlobalPropagator(e) {
      return (0, Ty.registerGlobal)(Py, e, gR.DiagAPI.instance());
    }
    inject(e, t, r = mR.defaultTextMapSetter) {
      return this._getGlobalPropagator().inject(e, t, r);
    }
    extract(e, t, r = mR.defaultTextMapGetter) {
      return this._getGlobalPropagator().extract(e, t, r);
    }
    fields() {
      return this._getGlobalPropagator().fields();
    }
    disable() {
      (0, Ty.unregisterGlobal)(Py, gR.DiagAPI.instance());
    }
    _getGlobalPropagator() {
      return (0, Ty.getGlobal)(Py) || Pq;
    }
  }
  hR.PropagationAPI = Iy;
});
var SR = k((_R) => {
  Object.defineProperty(_R, "__esModule", { value: true });
  _R.propagation = void 0;
  var Iq = bR();
  _R.propagation = Iq.PropagationAPI.getInstance();
});
var PR = k((ER) => {
  Object.defineProperty(ER, "__esModule", { value: true });
  ER.TraceAPI = void 0;
  var Ry = yo(), xR = gy(), wR = Pd(), Ci = uy(), kR = bo(), $y = "trace";
  class Oy {
    constructor() {
      this._proxyTracerProvider = new xR.ProxyTracerProvider(), this.wrapSpanContext = wR.wrapSpanContext, this.isSpanContextValid = wR.isSpanContextValid, this.deleteSpan = Ci.deleteSpan, this.getSpan = Ci.getSpan, this.getActiveSpan = Ci.getActiveSpan, this.getSpanContext = Ci.getSpanContext, this.setSpan = Ci.setSpan, this.setSpanContext = Ci.setSpanContext;
    }
    static getInstance() {
      if (!this._instance) this._instance = new Oy();
      return this._instance;
    }
    setGlobalTracerProvider(e) {
      let t = (0, Ry.registerGlobal)($y, this._proxyTracerProvider, kR.DiagAPI.instance());
      if (t) this._proxyTracerProvider.setDelegate(e);
      return t;
    }
    getTracerProvider() {
      return (0, Ry.getGlobal)($y) || this._proxyTracerProvider;
    }
    getTracer(e, t) {
      return this.getTracerProvider().getTracer(e, t);
    }
    disable() {
      (0, Ry.unregisterGlobal)($y, kR.DiagAPI.instance()), this._proxyTracerProvider = new xR.ProxyTracerProvider();
    }
  }
  ER.TraceAPI = Oy;
});
var $R = k((IR) => {
  Object.defineProperty(IR, "__esModule", { value: true });
  IR.trace = void 0;
  var Rq = PR();
  IR.trace = Rq.TraceAPI.getInstance();
});
var UR = k((be) => {
  Object.defineProperty(be, "__esModule", { value: true });
  be.trace = be.propagation = be.metrics = be.diag = be.context = be.INVALID_SPAN_CONTEXT = be.INVALID_TRACEID = be.INVALID_SPANID = be.isValidSpanId = be.isValidTraceId = be.isSpanContextValid = be.createTraceState = be.TraceFlags = be.SpanStatusCode = be.SpanKind = be.SamplingDecision = be.ProxyTracerProvider = be.ProxyTracer = be.defaultTextMapSetter = be.defaultTextMapGetter = be.ValueType = be.createNoopMeter = be.DiagLogLevel = be.DiagConsoleLogger = be.ROOT_CONTEXT = be.createContextKey = be.baggageEntryMetadataFromString = void 0;
  var $q = qh();
  Object.defineProperty(be, "baggageEntryMetadataFromString", { enumerable: true, get: function() {
    return $q.baggageEntryMetadataFromString;
  } });
  var OR = Ga();
  Object.defineProperty(be, "createContextKey", { enumerable: true, get: function() {
    return OR.createContextKey;
  } });
  Object.defineProperty(be, "ROOT_CONTEXT", { enumerable: true, get: function() {
    return OR.ROOT_CONTEXT;
  } });
  var Oq = PP();
  Object.defineProperty(be, "DiagConsoleLogger", { enumerable: true, get: function() {
    return Oq.DiagConsoleLogger;
  } });
  var Aq = xd();
  Object.defineProperty(be, "DiagLogLevel", { enumerable: true, get: function() {
    return Aq.DiagLogLevel;
  } });
  var Cq = ey();
  Object.defineProperty(be, "createNoopMeter", { enumerable: true, get: function() {
    return Cq.createNoopMeter;
  } });
  var Mq = zP();
  Object.defineProperty(be, "ValueType", { enumerable: true, get: function() {
    return Mq.ValueType;
  } });
  var AR = ry();
  Object.defineProperty(be, "defaultTextMapGetter", { enumerable: true, get: function() {
    return AR.defaultTextMapGetter;
  } });
  Object.defineProperty(be, "defaultTextMapSetter", { enumerable: true, get: function() {
    return AR.defaultTextMapSetter;
  } });
  var Dq = my();
  Object.defineProperty(be, "ProxyTracer", { enumerable: true, get: function() {
    return Dq.ProxyTracer;
  } });
  var Nq = gy();
  Object.defineProperty(be, "ProxyTracerProvider", { enumerable: true, get: function() {
    return Nq.ProxyTracerProvider;
  } });
  var jq = TI();
  Object.defineProperty(be, "SamplingDecision", { enumerable: true, get: function() {
    return jq.SamplingDecision;
  } });
  var Uq = II();
  Object.defineProperty(be, "SpanKind", { enumerable: true, get: function() {
    return Uq.SpanKind;
  } });
  var zq = $I();
  Object.defineProperty(be, "SpanStatusCode", { enumerable: true, get: function() {
    return zq.SpanStatusCode;
  } });
  var Lq = ay();
  Object.defineProperty(be, "TraceFlags", { enumerable: true, get: function() {
    return Lq.TraceFlags;
  } });
  var Fq = HI();
  Object.defineProperty(be, "createTraceState", { enumerable: true, get: function() {
    return Fq.createTraceState;
  } });
  var Ay = Pd();
  Object.defineProperty(be, "isSpanContextValid", { enumerable: true, get: function() {
    return Ay.isSpanContextValid;
  } });
  Object.defineProperty(be, "isValidTraceId", { enumerable: true, get: function() {
    return Ay.isValidTraceId;
  } });
  Object.defineProperty(be, "isValidSpanId", { enumerable: true, get: function() {
    return Ay.isValidSpanId;
  } });
  var Cy = Ed();
  Object.defineProperty(be, "INVALID_SPANID", { enumerable: true, get: function() {
    return Cy.INVALID_SPANID;
  } });
  Object.defineProperty(be, "INVALID_TRACEID", { enumerable: true, get: function() {
    return Cy.INVALID_TRACEID;
  } });
  Object.defineProperty(be, "INVALID_SPAN_CONTEXT", { enumerable: true, get: function() {
    return Cy.INVALID_SPAN_CONTEXT;
  } });
  var CR = VI();
  Object.defineProperty(be, "context", { enumerable: true, get: function() {
    return CR.context;
  } });
  var MR = GI();
  Object.defineProperty(be, "diag", { enumerable: true, get: function() {
    return MR.diag;
  } });
  var DR = iR();
  Object.defineProperty(be, "metrics", { enumerable: true, get: function() {
    return DR.metrics;
  } });
  var NR = SR();
  Object.defineProperty(be, "propagation", { enumerable: true, get: function() {
    return NR.propagation;
  } });
  var jR = $R();
  Object.defineProperty(be, "trace", { enumerable: true, get: function() {
    return jR.trace;
  } });
  be.default = { context: CR.context, diag: MR.diag, metrics: DR.metrics, propagation: NR.propagation, trace: jR.trace };
});
var $l = k((zO) => {
  Object.defineProperty(zO, "__esModule", { value: true });
  zO.regexpCode = zO.getEsmExportName = zO.getProperty = zO.safeStringify = zO.stringify = zO.strConcat = zO.addCodeArg = zO.str = zO._ = zO.nil = zO._Code = zO.Name = zO.IDENTIFIER = zO._CodeOrName = void 0;
  class vm {
  }
  zO._CodeOrName = vm;
  zO.IDENTIFIER = /^[a-z$_][a-z$_0-9]*$/i;
  class gs extends vm {
    constructor(e) {
      super();
      if (!zO.IDENTIFIER.test(e)) throw Error("CodeGen: name must be a valid identifier");
      this.str = e;
    }
    toString() {
      return this.str;
    }
    emptyStr() {
      return false;
    }
    get names() {
      return { [this.str]: 1 };
    }
  }
  zO.Name = gs;
  class fr extends vm {
    constructor(e) {
      super();
      this._items = typeof e === "string" ? [e] : e;
    }
    toString() {
      return this.str;
    }
    emptyStr() {
      if (this._items.length > 1) return false;
      let e = this._items[0];
      return e === "" || e === '""';
    }
    get str() {
      var e;
      return (e = this._str) !== null && e !== void 0 ? e : this._str = this._items.reduce((t, r) => `${t}${r}`, "");
    }
    get names() {
      var e;
      return (e = this._names) !== null && e !== void 0 ? e : this._names = this._items.reduce((t, r) => {
        if (r instanceof gs) t[r.str] = (t[r.str] || 0) + 1;
        return t;
      }, {});
    }
  }
  zO._Code = fr;
  zO.nil = new fr("");
  function jO(e, ...t) {
    let r = [e[0]], o = 0;
    while (o < t.length) _S(r, t[o]), r.push(e[++o]);
    return new fr(r);
  }
  zO._ = jO;
  var bS = new fr("+");
  function UO(e, ...t) {
    let r = [Rl(e[0])], o = 0;
    while (o < t.length) r.push(bS), _S(r, t[o]), r.push(bS, Rl(e[++o]));
    return a9(r), new fr(r);
  }
  zO.str = UO;
  function _S(e, t) {
    if (t instanceof fr) e.push(...t._items);
    else if (t instanceof gs) e.push(t);
    else e.push(u9(t));
  }
  zO.addCodeArg = _S;
  function a9(e) {
    let t = 1;
    while (t < e.length - 1) {
      if (e[t] === bS) {
        let r = c9(e[t - 1], e[t + 1]);
        if (r !== void 0) {
          e.splice(t - 1, 3, r);
          continue;
        }
        e[t++] = "+";
      }
      t++;
    }
  }
  function c9(e, t) {
    if (t === '""') return e;
    if (e === '""') return t;
    if (typeof e == "string") {
      if (t instanceof gs || e[e.length - 1] !== '"') return;
      if (typeof t != "string") return `${e.slice(0, -1)}${t}"`;
      if (t[0] === '"') return e.slice(0, -1) + t.slice(1);
      return;
    }
    if (typeof t == "string" && t[0] === '"' && !(e instanceof gs)) return `"${e}${t.slice(1)}`;
    return;
  }
  function l9(e, t) {
    return t.emptyStr() ? e : e.emptyStr() ? t : UO`${e}${t}`;
  }
  zO.strConcat = l9;
  function u9(e) {
    return typeof e == "number" || typeof e == "boolean" || e === null ? e : Rl(Array.isArray(e) ? e.join(",") : e);
  }
  function d9(e) {
    return new fr(Rl(e));
  }
  zO.stringify = d9;
  function Rl(e) {
    return JSON.stringify(e).replace(/\u2028/g, "\\u2028").replace(/\u2029/g, "\\u2029");
  }
  zO.safeStringify = Rl;
  function p9(e) {
    return typeof e == "string" && zO.IDENTIFIER.test(e) ? new fr(`.${e}`) : jO`[${e}]`;
  }
  zO.getProperty = p9;
  function f9(e) {
    if (typeof e == "string" && zO.IDENTIFIER.test(e)) return new fr(`${e}`);
    throw Error(`CodeGen: invalid export name: ${e}, use explicit $id name mapping`);
  }
  zO.getEsmExportName = f9;
  function m9(e) {
    return new fr(e.toString());
  }
  zO.regexpCode = m9;
});
var wS = k((HO) => {
  Object.defineProperty(HO, "__esModule", { value: true });
  HO.ValueScope = HO.ValueScopeName = HO.Scope = HO.varKinds = HO.UsedValueState = void 0;
  var Tt = $l();
  class FO extends Error {
    constructor(e) {
      super(`CodeGen: "code" for ${e} not defined`);
      this.value = e.value;
    }
  }
  var xm;
  (function(e) {
    e[e.Started = 0] = "Started", e[e.Completed = 1] = "Completed";
  })(xm || (HO.UsedValueState = xm = {}));
  HO.varKinds = { const: new Tt.Name("const"), let: new Tt.Name("let"), var: new Tt.Name("var") };
  class SS {
    constructor({ prefixes: e, parent: t } = {}) {
      this._names = {}, this._prefixes = e, this._parent = t;
    }
    toName(e) {
      return e instanceof Tt.Name ? e : this.name(e);
    }
    name(e) {
      return new Tt.Name(this._newName(e));
    }
    _newName(e) {
      let t = this._names[e] || this._nameGroup(e);
      return `${e}${t.index++}`;
    }
    _nameGroup(e) {
      var t, r;
      if (((r = (t = this._parent) === null || t === void 0 ? void 0 : t._prefixes) === null || r === void 0 ? void 0 : r.has(e)) || this._prefixes && !this._prefixes.has(e)) throw Error(`CodeGen: prefix "${e}" is not allowed in this scope`);
      return this._names[e] = { prefix: e, index: 0 };
    }
  }
  HO.Scope = SS;
  class xS extends Tt.Name {
    constructor(e, t) {
      super(t);
      this.prefix = e;
    }
    setValue(e, { property: t, itemIndex: r }) {
      this.value = e, this.scopePath = Tt._`.${new Tt.Name(t)}[${r}]`;
    }
  }
  HO.ValueScopeName = xS;
  var P9 = Tt._`\n`;
  class BO extends SS {
    constructor(e) {
      super(e);
      this._values = {}, this._scope = e.scope, this.opts = { ...e, _n: e.lines ? P9 : Tt.nil };
    }
    get() {
      return this._scope;
    }
    name(e) {
      return new xS(e, this._newName(e));
    }
    value(e, t) {
      var r;
      if (t.ref === void 0) throw Error("CodeGen: ref must be passed in value");
      let o = this.toName(e), { prefix: n } = o, i = (r = t.key) !== null && r !== void 0 ? r : t.ref, s = this._values[n];
      if (s) {
        let u = s.get(i);
        if (u) return u;
      } else s = this._values[n] = /* @__PURE__ */ new Map();
      s.set(i, o);
      let a = this._scope[n] || (this._scope[n] = []), c = a.length;
      return a[c] = t.ref, o.setValue(t, { property: n, itemIndex: c }), o;
    }
    getValue(e, t) {
      let r = this._values[e];
      if (!r) return;
      return r.get(t);
    }
    scopeRefs(e, t = this._values) {
      return this._reduceValues(t, (r) => {
        if (r.scopePath === void 0) throw Error(`CodeGen: name "${r}" has no value`);
        return Tt._`${e}${r.scopePath}`;
      });
    }
    scopeCode(e = this._values, t, r) {
      return this._reduceValues(e, (o) => {
        if (o.value === void 0) throw Error(`CodeGen: name "${o}" has no value`);
        return o.value.code;
      }, t, r);
    }
    _reduceValues(e, t, r = {}, o) {
      let n = Tt.nil;
      for (let i in e) {
        let s = e[i];
        if (!s) continue;
        let a = r[i] = r[i] || /* @__PURE__ */ new Map();
        s.forEach((c) => {
          if (a.has(c)) return;
          a.set(c, xm.Started);
          let u = t(c);
          if (u) {
            let d = this.opts.es5 ? HO.varKinds.var : HO.varKinds.const;
            n = Tt._`${n}${d} ${c} = ${u};${this.opts._n}`;
          } else if (u = o === null || o === void 0 ? void 0 : o(c)) n = Tt._`${n}${u}${this.opts._n}`;
          else throw new FO(c);
          a.set(c, xm.Completed);
        });
      }
      return n;
    }
  }
  HO.ValueScope = BO;
});
var re = k((Pt) => {
  Object.defineProperty(Pt, "__esModule", { value: true });
  Pt.or = Pt.and = Pt.not = Pt.CodeGen = Pt.operators = Pt.varKinds = Pt.ValueScopeName = Pt.ValueScope = Pt.Scope = Pt.Name = Pt.regexpCode = Pt.stringify = Pt.getProperty = Pt.nil = Pt.strConcat = Pt.str = Pt._ = void 0;
  var le = $l(), mr = wS(), Un = $l();
  Object.defineProperty(Pt, "_", { enumerable: true, get: function() {
    return Un._;
  } });
  Object.defineProperty(Pt, "str", { enumerable: true, get: function() {
    return Un.str;
  } });
  Object.defineProperty(Pt, "strConcat", { enumerable: true, get: function() {
    return Un.strConcat;
  } });
  Object.defineProperty(Pt, "nil", { enumerable: true, get: function() {
    return Un.nil;
  } });
  Object.defineProperty(Pt, "getProperty", { enumerable: true, get: function() {
    return Un.getProperty;
  } });
  Object.defineProperty(Pt, "stringify", { enumerable: true, get: function() {
    return Un.stringify;
  } });
  Object.defineProperty(Pt, "regexpCode", { enumerable: true, get: function() {
    return Un.regexpCode;
  } });
  Object.defineProperty(Pt, "Name", { enumerable: true, get: function() {
    return Un.Name;
  } });
  var Im = wS();
  Object.defineProperty(Pt, "Scope", { enumerable: true, get: function() {
    return Im.Scope;
  } });
  Object.defineProperty(Pt, "ValueScope", { enumerable: true, get: function() {
    return Im.ValueScope;
  } });
  Object.defineProperty(Pt, "ValueScopeName", { enumerable: true, get: function() {
    return Im.ValueScopeName;
  } });
  Object.defineProperty(Pt, "varKinds", { enumerable: true, get: function() {
    return Im.varKinds;
  } });
  Pt.operators = { GT: new le._Code(">"), GTE: new le._Code(">="), LT: new le._Code("<"), LTE: new le._Code("<="), EQ: new le._Code("==="), NEQ: new le._Code("!=="), NOT: new le._Code("!"), OR: new le._Code("||"), AND: new le._Code("&&"), ADD: new le._Code("+") };
  class zn {
    optimizeNodes() {
      return this;
    }
    optimizeNames(e, t) {
      return this;
    }
  }
  class ZO extends zn {
    constructor(e, t, r) {
      super();
      this.varKind = e, this.name = t, this.rhs = r;
    }
    render({ es5: e, _n: t }) {
      let r = e ? mr.varKinds.var : this.varKind, o = this.rhs === void 0 ? "" : ` = ${this.rhs}`;
      return `${r} ${this.name}${o};` + t;
    }
    optimizeNames(e, t) {
      if (!e[this.name.str]) return;
      if (this.rhs) this.rhs = ys(this.rhs, e, t);
      return this;
    }
    get names() {
      return this.rhs instanceof le._CodeOrName ? this.rhs.names : {};
    }
  }
  class TS extends zn {
    constructor(e, t, r) {
      super();
      this.lhs = e, this.rhs = t, this.sideEffects = r;
    }
    render({ _n: e }) {
      return `${this.lhs} = ${this.rhs};` + e;
    }
    optimizeNames(e, t) {
      if (this.lhs instanceof le.Name && !e[this.lhs.str] && !this.sideEffects) return;
      return this.rhs = ys(this.rhs, e, t), this;
    }
    get names() {
      let e = this.lhs instanceof le.Name ? {} : { ...this.lhs.names };
      return Pm(e, this.rhs);
    }
  }
  class VO extends TS {
    constructor(e, t, r, o) {
      super(e, r, o);
      this.op = t;
    }
    render({ _n: e }) {
      return `${this.lhs} ${this.op}= ${this.rhs};` + e;
    }
  }
  class WO extends zn {
    constructor(e) {
      super();
      this.label = e, this.names = {};
    }
    render({ _n: e }) {
      return `${this.label}:` + e;
    }
  }
  class KO extends zn {
    constructor(e) {
      super();
      this.label = e, this.names = {};
    }
    render({ _n: e }) {
      return `break${this.label ? ` ${this.label}` : ""};` + e;
    }
  }
  class GO extends zn {
    constructor(e) {
      super();
      this.error = e;
    }
    render({ _n: e }) {
      return `throw ${this.error};` + e;
    }
    get names() {
      return this.error.names;
    }
  }
  class JO extends zn {
    constructor(e) {
      super();
      this.code = e;
    }
    render({ _n: e }) {
      return `${this.code};` + e;
    }
    optimizeNodes() {
      return `${this.code}` ? this : void 0;
    }
    optimizeNames(e, t) {
      return this.code = ys(this.code, e, t), this;
    }
    get names() {
      return this.code instanceof le._CodeOrName ? this.code.names : {};
    }
  }
  class Rm extends zn {
    constructor(e = []) {
      super();
      this.nodes = e;
    }
    render(e) {
      return this.nodes.reduce((t, r) => t + r.render(e), "");
    }
    optimizeNodes() {
      let { nodes: e } = this, t = e.length;
      while (t--) {
        let r = e[t].optimizeNodes();
        if (Array.isArray(r)) e.splice(t, 1, ...r);
        else if (r) e[t] = r;
        else e.splice(t, 1);
      }
      return e.length > 0 ? this : void 0;
    }
    optimizeNames(e, t) {
      let { nodes: r } = this, o = r.length;
      while (o--) {
        let n = r[o];
        if (n.optimizeNames(e, t)) continue;
        O9(e, n.names), r.splice(o, 1);
      }
      return r.length > 0 ? this : void 0;
    }
    get names() {
      return this.nodes.reduce((e, t) => Do(e, t.names), {});
    }
  }
  class Ln extends Rm {
    render(e) {
      return "{" + e._n + super.render(e) + "}" + e._n;
    }
  }
  class XO extends Rm {
  }
  class Ol extends Ln {
  }
  Ol.kind = "else";
  class en extends Ln {
    constructor(e, t) {
      super(t);
      this.condition = e;
    }
    render(e) {
      let t = `if(${this.condition})` + super.render(e);
      if (this.else) t += "else " + this.else.render(e);
      return t;
    }
    optimizeNodes() {
      super.optimizeNodes();
      let e = this.condition;
      if (e === true) return this.nodes;
      let t = this.else;
      if (t) {
        let r = t.optimizeNodes();
        t = this.else = Array.isArray(r) ? new Ol(r) : r;
      }
      if (t) {
        if (e === false) return t instanceof en ? t : t.nodes;
        if (this.nodes.length) return this;
        return new en(rA(e), t instanceof en ? [t] : t.nodes);
      }
      if (e === false || !this.nodes.length) return;
      return this;
    }
    optimizeNames(e, t) {
      var r;
      if (this.else = (r = this.else) === null || r === void 0 ? void 0 : r.optimizeNames(e, t), !(super.optimizeNames(e, t) || this.else)) return;
      return this.condition = ys(this.condition, e, t), this;
    }
    get names() {
      let e = super.names;
      if (Pm(e, this.condition), this.else) Do(e, this.else.names);
      return e;
    }
  }
  en.kind = "if";
  class hs extends Ln {
  }
  hs.kind = "for";
  class YO extends hs {
    constructor(e) {
      super();
      this.iteration = e;
    }
    render(e) {
      return `for(${this.iteration})` + super.render(e);
    }
    optimizeNames(e, t) {
      if (!super.optimizeNames(e, t)) return;
      return this.iteration = ys(this.iteration, e, t), this;
    }
    get names() {
      return Do(super.names, this.iteration.names);
    }
  }
  class QO extends hs {
    constructor(e, t, r, o) {
      super();
      this.varKind = e, this.name = t, this.from = r, this.to = o;
    }
    render(e) {
      let t = e.es5 ? mr.varKinds.var : this.varKind, { name: r, from: o, to: n } = this;
      return `for(${t} ${r}=${o}; ${r}<${n}; ${r}++)` + super.render(e);
    }
    get names() {
      let e = Pm(super.names, this.from);
      return Pm(e, this.to);
    }
  }
  class kS extends hs {
    constructor(e, t, r, o) {
      super();
      this.loop = e, this.varKind = t, this.name = r, this.iterable = o;
    }
    render(e) {
      return `for(${this.varKind} ${this.name} ${this.loop} ${this.iterable})` + super.render(e);
    }
    optimizeNames(e, t) {
      if (!super.optimizeNames(e, t)) return;
      return this.iterable = ys(this.iterable, e, t), this;
    }
    get names() {
      return Do(super.names, this.iterable.names);
    }
  }
  class wm extends Ln {
    constructor(e, t, r) {
      super();
      this.name = e, this.args = t, this.async = r;
    }
    render(e) {
      return `${this.async ? "async " : ""}function ${this.name}(${this.args})` + super.render(e);
    }
  }
  wm.kind = "func";
  class km extends Rm {
    render(e) {
      return "return " + super.render(e);
    }
  }
  km.kind = "return";
  class eA extends Ln {
    render(e) {
      let t = "try" + super.render(e);
      if (this.catch) t += this.catch.render(e);
      if (this.finally) t += this.finally.render(e);
      return t;
    }
    optimizeNodes() {
      var e, t;
      return super.optimizeNodes(), (e = this.catch) === null || e === void 0 || e.optimizeNodes(), (t = this.finally) === null || t === void 0 || t.optimizeNodes(), this;
    }
    optimizeNames(e, t) {
      var r, o;
      return super.optimizeNames(e, t), (r = this.catch) === null || r === void 0 || r.optimizeNames(e, t), (o = this.finally) === null || o === void 0 || o.optimizeNames(e, t), this;
    }
    get names() {
      let e = super.names;
      if (this.catch) Do(e, this.catch.names);
      if (this.finally) Do(e, this.finally.names);
      return e;
    }
  }
  class Em extends Ln {
    constructor(e) {
      super();
      this.error = e;
    }
    render(e) {
      return `catch(${this.error})` + super.render(e);
    }
  }
  Em.kind = "catch";
  class Tm extends Ln {
    render(e) {
      return "finally" + super.render(e);
    }
  }
  Tm.kind = "finally";
  class tA {
    constructor(e, t = {}) {
      this._values = {}, this._blockStarts = [], this._constants = {}, this.opts = { ...t, _n: t.lines ? `
` : "" }, this._extScope = e, this._scope = new mr.Scope({ parent: e }), this._nodes = [new XO()];
    }
    toString() {
      return this._root.render(this.opts);
    }
    name(e) {
      return this._scope.name(e);
    }
    scopeName(e) {
      return this._extScope.name(e);
    }
    scopeValue(e, t) {
      let r = this._extScope.value(e, t);
      return (this._values[r.prefix] || (this._values[r.prefix] = /* @__PURE__ */ new Set())).add(r), r;
    }
    getScopeValue(e, t) {
      return this._extScope.getValue(e, t);
    }
    scopeRefs(e) {
      return this._extScope.scopeRefs(e, this._values);
    }
    scopeCode() {
      return this._extScope.scopeCode(this._values);
    }
    _def(e, t, r, o) {
      let n = this._scope.toName(t);
      if (r !== void 0 && o) this._constants[n.str] = r;
      return this._leafNode(new ZO(e, n, r)), n;
    }
    const(e, t, r) {
      return this._def(mr.varKinds.const, e, t, r);
    }
    let(e, t, r) {
      return this._def(mr.varKinds.let, e, t, r);
    }
    var(e, t, r) {
      return this._def(mr.varKinds.var, e, t, r);
    }
    assign(e, t, r) {
      return this._leafNode(new TS(e, t, r));
    }
    add(e, t) {
      return this._leafNode(new VO(e, Pt.operators.ADD, t));
    }
    code(e) {
      if (typeof e == "function") e();
      else if (e !== le.nil) this._leafNode(new JO(e));
      return this;
    }
    object(...e) {
      let t = ["{"];
      for (let [r, o] of e) {
        if (t.length > 1) t.push(",");
        if (t.push(r), r !== o || this.opts.es5) t.push(":"), (0, le.addCodeArg)(t, o);
      }
      return t.push("}"), new le._Code(t);
    }
    if(e, t, r) {
      if (this._blockNode(new en(e)), t && r) this.code(t).else().code(r).endIf();
      else if (t) this.code(t).endIf();
      else if (r) throw Error('CodeGen: "else" body without "then" body');
      return this;
    }
    elseIf(e) {
      return this._elseNode(new en(e));
    }
    else() {
      return this._elseNode(new Ol());
    }
    endIf() {
      return this._endBlockNode(en, Ol);
    }
    _for(e, t) {
      if (this._blockNode(e), t) this.code(t).endFor();
      return this;
    }
    for(e, t) {
      return this._for(new YO(e), t);
    }
    forRange(e, t, r, o, n = this.opts.es5 ? mr.varKinds.var : mr.varKinds.let) {
      let i = this._scope.toName(e);
      return this._for(new QO(n, i, t, r), () => o(i));
    }
    forOf(e, t, r, o = mr.varKinds.const) {
      let n = this._scope.toName(e);
      if (this.opts.es5) {
        let i = t instanceof le.Name ? t : this.var("_arr", t);
        return this.forRange("_i", 0, le._`${i}.length`, (s) => {
          this.var(n, le._`${i}[${s}]`), r(n);
        });
      }
      return this._for(new kS("of", o, n, t), () => r(n));
    }
    forIn(e, t, r, o = this.opts.es5 ? mr.varKinds.var : mr.varKinds.const) {
      if (this.opts.ownProperties) return this.forOf(e, le._`Object.keys(${t})`, r);
      let n = this._scope.toName(e);
      return this._for(new kS("in", o, n, t), () => r(n));
    }
    endFor() {
      return this._endBlockNode(hs);
    }
    label(e) {
      return this._leafNode(new WO(e));
    }
    break(e) {
      return this._leafNode(new KO(e));
    }
    return(e) {
      let t = new km();
      if (this._blockNode(t), this.code(e), t.nodes.length !== 1) throw Error('CodeGen: "return" should have one node');
      return this._endBlockNode(km);
    }
    try(e, t, r) {
      if (!t && !r) throw Error('CodeGen: "try" without "catch" and "finally"');
      let o = new eA();
      if (this._blockNode(o), this.code(e), t) {
        let n = this.name("e");
        this._currNode = o.catch = new Em(n), t(n);
      }
      if (r) this._currNode = o.finally = new Tm(), this.code(r);
      return this._endBlockNode(Em, Tm);
    }
    throw(e) {
      return this._leafNode(new GO(e));
    }
    block(e, t) {
      if (this._blockStarts.push(this._nodes.length), e) this.code(e).endBlock(t);
      return this;
    }
    endBlock(e) {
      let t = this._blockStarts.pop();
      if (t === void 0) throw Error("CodeGen: not in self-balancing block");
      let r = this._nodes.length - t;
      if (r < 0 || e !== void 0 && r !== e) throw Error(`CodeGen: wrong number of nodes: ${r} vs ${e} expected`);
      return this._nodes.length = t, this;
    }
    func(e, t = le.nil, r, o) {
      if (this._blockNode(new wm(e, t, r)), o) this.code(o).endFunc();
      return this;
    }
    endFunc() {
      return this._endBlockNode(wm);
    }
    optimize(e = 1) {
      while (e-- > 0) this._root.optimizeNodes(), this._root.optimizeNames(this._root.names, this._constants);
    }
    _leafNode(e) {
      return this._currNode.nodes.push(e), this;
    }
    _blockNode(e) {
      this._currNode.nodes.push(e), this._nodes.push(e);
    }
    _endBlockNode(e, t) {
      let r = this._currNode;
      if (r instanceof e || t && r instanceof t) return this._nodes.pop(), this;
      throw Error(`CodeGen: not in block "${t ? `${e.kind}/${t.kind}` : e.kind}"`);
    }
    _elseNode(e) {
      let t = this._currNode;
      if (!(t instanceof en)) throw Error('CodeGen: "else" without "if"');
      return this._currNode = t.else = e, this;
    }
    get _root() {
      return this._nodes[0];
    }
    get _currNode() {
      let e = this._nodes;
      return e[e.length - 1];
    }
    set _currNode(e) {
      let t = this._nodes;
      t[t.length - 1] = e;
    }
  }
  Pt.CodeGen = tA;
  function Do(e, t) {
    for (let r in t) e[r] = (e[r] || 0) + (t[r] || 0);
    return e;
  }
  function Pm(e, t) {
    return t instanceof le._CodeOrName ? Do(e, t.names) : e;
  }
  function ys(e, t, r) {
    if (e instanceof le.Name) return o(e);
    if (!n(e)) return e;
    return new le._Code(e._items.reduce((i, s) => {
      if (s instanceof le.Name) s = o(s);
      if (s instanceof le._Code) i.push(...s._items);
      else i.push(s);
      return i;
    }, []));
    function o(i) {
      let s = r[i.str];
      if (s === void 0 || t[i.str] !== 1) return i;
      return delete t[i.str], s;
    }
    function n(i) {
      return i instanceof le._Code && i._items.some((s) => s instanceof le.Name && t[s.str] === 1 && r[s.str] !== void 0);
    }
  }
  function O9(e, t) {
    for (let r in t) e[r] = (e[r] || 0) - (t[r] || 0);
  }
  function rA(e) {
    return typeof e == "boolean" || typeof e == "number" || e === null ? !e : le._`!${ES(e)}`;
  }
  Pt.not = rA;
  var A9 = nA(Pt.operators.AND);
  function C9(...e) {
    return e.reduce(A9);
  }
  Pt.and = C9;
  var M9 = nA(Pt.operators.OR);
  function D9(...e) {
    return e.reduce(M9);
  }
  Pt.or = D9;
  function nA(e) {
    return (t, r) => t === le.nil ? r : r === le.nil ? t : le._`${ES(t)} ${e} ${ES(r)}`;
  }
  function ES(e) {
    return e instanceof le.Name ? e : le._`(${e})`;
  }
});
var ue = k((pA) => {
  Object.defineProperty(pA, "__esModule", { value: true });
  pA.checkStrictMode = pA.getErrorPath = pA.Type = pA.useFunc = pA.setEvaluated = pA.evaluatedPropsToName = pA.mergeEvaluated = pA.eachItem = pA.unescapeJsonPointer = pA.escapeJsonPointer = pA.escapeFragment = pA.unescapeFragment = pA.schemaRefOrVal = pA.schemaHasRulesButRef = pA.schemaHasRules = pA.checkUnknownRules = pA.alwaysValidSchema = pA.toHash = void 0;
  var Te = re(), z9 = $l();
  function L9(e) {
    let t = {};
    for (let r of e) t[r] = true;
    return t;
  }
  pA.toHash = L9;
  function F9(e, t) {
    if (typeof t == "boolean") return t;
    if (Object.keys(t).length === 0) return true;
    return aA(e, t), !cA(t, e.self.RULES.all);
  }
  pA.alwaysValidSchema = F9;
  function aA(e, t = e.schema) {
    let { opts: r, self: o } = e;
    if (!r.strictSchema) return;
    if (typeof t === "boolean") return;
    let n = o.RULES.keywords;
    for (let i in t) if (!n[i]) dA(e, `unknown keyword: "${i}"`);
  }
  pA.checkUnknownRules = aA;
  function cA(e, t) {
    if (typeof e == "boolean") return !e;
    for (let r in e) if (t[r]) return true;
    return false;
  }
  pA.schemaHasRules = cA;
  function B9(e, t) {
    if (typeof e == "boolean") return !e;
    for (let r in e) if (r !== "$ref" && t.all[r]) return true;
    return false;
  }
  pA.schemaHasRulesButRef = B9;
  function H9({ topSchemaRef: e, schemaPath: t }, r, o, n) {
    if (!n) {
      if (typeof r == "number" || typeof r == "boolean") return r;
      if (typeof r == "string") return Te._`${r}`;
    }
    return Te._`${e}${t}${(0, Te.getProperty)(o)}`;
  }
  pA.schemaRefOrVal = H9;
  function q9(e) {
    return lA(decodeURIComponent(e));
  }
  pA.unescapeFragment = q9;
  function Z9(e) {
    return encodeURIComponent(IS(e));
  }
  pA.escapeFragment = Z9;
  function IS(e) {
    if (typeof e == "number") return `${e}`;
    return e.replace(/~/g, "~0").replace(/\//g, "~1");
  }
  pA.escapeJsonPointer = IS;
  function lA(e) {
    return e.replace(/~1/g, "/").replace(/~0/g, "~");
  }
  pA.unescapeJsonPointer = lA;
  function V9(e, t) {
    if (Array.isArray(e)) for (let r of e) t(r);
    else t(e);
  }
  pA.eachItem = V9;
  function iA({ mergeNames: e, mergeToName: t, mergeValues: r, resultToName: o }) {
    return (n, i, s, a) => {
      let c = s === void 0 ? i : s instanceof Te.Name ? (i instanceof Te.Name ? e(n, i, s) : t(n, i, s), s) : i instanceof Te.Name ? (t(n, s, i), i) : r(i, s);
      return a === Te.Name && !(c instanceof Te.Name) ? o(n, c) : c;
    };
  }
  pA.mergeEvaluated = { props: iA({ mergeNames: (e, t, r) => e.if(Te._`${r} !== true && ${t} !== undefined`, () => {
    e.if(Te._`${t} === true`, () => e.assign(r, true), () => e.assign(r, Te._`${r} || {}`).code(Te._`Object.assign(${r}, ${t})`));
  }), mergeToName: (e, t, r) => e.if(Te._`${r} !== true`, () => {
    if (t === true) e.assign(r, true);
    else e.assign(r, Te._`${r} || {}`), RS(e, r, t);
  }), mergeValues: (e, t) => e === true ? true : { ...e, ...t }, resultToName: uA }), items: iA({ mergeNames: (e, t, r) => e.if(Te._`${r} !== true && ${t} !== undefined`, () => e.assign(r, Te._`${t} === true ? true : ${r} > ${t} ? ${r} : ${t}`)), mergeToName: (e, t, r) => e.if(Te._`${r} !== true`, () => e.assign(r, t === true ? true : Te._`${r} > ${t} ? ${r} : ${t}`)), mergeValues: (e, t) => e === true ? true : Math.max(e, t), resultToName: (e, t) => e.var("items", t) }) };
  function uA(e, t) {
    if (t === true) return e.var("props", true);
    let r = e.var("props", Te._`{}`);
    if (t !== void 0) RS(e, r, t);
    return r;
  }
  pA.evaluatedPropsToName = uA;
  function RS(e, t, r) {
    Object.keys(r).forEach((o) => e.assign(Te._`${t}${(0, Te.getProperty)(o)}`, true));
  }
  pA.setEvaluated = RS;
  var sA = {};
  function W9(e, t) {
    return e.scopeValue("func", { ref: t, code: sA[t.code] || (sA[t.code] = new z9._Code(t.code)) });
  }
  pA.useFunc = W9;
  var PS;
  (function(e) {
    e[e.Num = 0] = "Num", e[e.Str = 1] = "Str";
  })(PS || (pA.Type = PS = {}));
  function K9(e, t, r) {
    if (e instanceof Te.Name) {
      let o = t === PS.Num;
      return r ? o ? Te._`"[" + ${e} + "]"` : Te._`"['" + ${e} + "']"` : o ? Te._`"/" + ${e}` : Te._`"/" + ${e}.replace(/~/g, "~0").replace(/\\//g, "~1")`;
    }
    return r ? (0, Te.getProperty)(e).toString() : "/" + IS(e);
  }
  pA.getErrorPath = K9;
  function dA(e, t, r = e.opts.strictSchema) {
    if (!r) return;
    if (t = `strict mode: ${t}`, r === true) throw Error(t);
    e.self.logger.warn(t);
  }
  pA.checkStrictMode = dA;
});
var tn = k((mA) => {
  Object.defineProperty(mA, "__esModule", { value: true });
  var lt = re(), pJ = { data: new lt.Name("data"), valCxt: new lt.Name("valCxt"), instancePath: new lt.Name("instancePath"), parentData: new lt.Name("parentData"), parentDataProperty: new lt.Name("parentDataProperty"), rootData: new lt.Name("rootData"), dynamicAnchors: new lt.Name("dynamicAnchors"), vErrors: new lt.Name("vErrors"), errors: new lt.Name("errors"), this: new lt.Name("this"), self: new lt.Name("self"), scope: new lt.Name("scope"), json: new lt.Name("json"), jsonPos: new lt.Name("jsonPos"), jsonLen: new lt.Name("jsonLen"), jsonPart: new lt.Name("jsonPart") };
  mA.default = pJ;
});
var Al = k((bA) => {
  Object.defineProperty(bA, "__esModule", { value: true });
  bA.extendErrors = bA.resetErrorsCount = bA.reportExtraError = bA.reportError = bA.keyword$DataError = bA.keywordError = void 0;
  var de = re(), Om = ue(), gt = tn();
  bA.keywordError = { message: ({ keyword: e }) => de.str`must pass "${e}" keyword validation` };
  bA.keyword$DataError = { message: ({ keyword: e, schemaType: t }) => t ? de.str`"${e}" keyword must be ${t} ($data)` : de.str`"${e}" keyword is invalid ($data)` };
  function mJ(e, t = bA.keywordError, r, o) {
    let { it: n } = e, { gen: i, compositeRule: s, allErrors: a } = n, c = yA(e, t, r);
    if (o !== null && o !== void 0 ? o : s || a) gA(i, c);
    else hA(n, de._`[${c}]`);
  }
  bA.reportError = mJ;
  function gJ(e, t = bA.keywordError, r) {
    let { it: o } = e, { gen: n, compositeRule: i, allErrors: s } = o, a = yA(e, t, r);
    if (gA(n, a), !(i || s)) hA(o, gt.default.vErrors);
  }
  bA.reportExtraError = gJ;
  function hJ(e, t) {
    e.assign(gt.default.errors, t), e.if(de._`${gt.default.vErrors} !== null`, () => e.if(t, () => e.assign(de._`${gt.default.vErrors}.length`, t), () => e.assign(gt.default.vErrors, null)));
  }
  bA.resetErrorsCount = hJ;
  function yJ({ gen: e, keyword: t, schemaValue: r, data: o, errsCount: n, it: i }) {
    if (n === void 0) throw Error("ajv implementation error");
    let s = e.name("err");
    e.forRange("i", n, gt.default.errors, (a) => {
      if (e.const(s, de._`${gt.default.vErrors}[${a}]`), e.if(de._`${s}.instancePath === undefined`, () => e.assign(de._`${s}.instancePath`, (0, de.strConcat)(gt.default.instancePath, i.errorPath))), e.assign(de._`${s}.schemaPath`, de.str`${i.errSchemaPath}/${t}`), i.opts.verbose) e.assign(de._`${s}.schema`, r), e.assign(de._`${s}.data`, o);
    });
  }
  bA.extendErrors = yJ;
  function gA(e, t) {
    let r = e.const("err", t);
    e.if(de._`${gt.default.vErrors} === null`, () => e.assign(gt.default.vErrors, de._`[${r}]`), de._`${gt.default.vErrors}.push(${r})`), e.code(de._`${gt.default.errors}++`);
  }
  function hA(e, t) {
    let { gen: r, validateName: o, schemaEnv: n } = e;
    if (n.$async) r.throw(de._`new ${e.ValidationError}(${t})`);
    else r.assign(de._`${o}.errors`, t), r.return(false);
  }
  var No = { keyword: new de.Name("keyword"), schemaPath: new de.Name("schemaPath"), params: new de.Name("params"), propertyName: new de.Name("propertyName"), message: new de.Name("message"), schema: new de.Name("schema"), parentSchema: new de.Name("parentSchema") };
  function yA(e, t, r) {
    let { createErrors: o } = e.it;
    if (o === false) return de._`{}`;
    return bJ(e, t, r);
  }
  function bJ(e, t, r = {}) {
    let { gen: o, it: n } = e, i = [_J(n, r), vJ(e, r)];
    return SJ(e, t, i), o.object(...i);
  }
  function _J({ errorPath: e }, { instancePath: t }) {
    let r = t ? de.str`${e}${(0, Om.getErrorPath)(t, Om.Type.Str)}` : e;
    return [gt.default.instancePath, (0, de.strConcat)(gt.default.instancePath, r)];
  }
  function vJ({ keyword: e, it: { errSchemaPath: t } }, { schemaPath: r, parentSchema: o }) {
    let n = o ? t : de.str`${t}/${e}`;
    if (r) n = de.str`${n}${(0, Om.getErrorPath)(r, Om.Type.Str)}`;
    return [No.schemaPath, n];
  }
  function SJ(e, { params: t, message: r }, o) {
    let { keyword: n, data: i, schemaValue: s, it: a } = e, { opts: c, propertyName: u, topSchemaRef: d, schemaPath: p } = a;
    if (o.push([No.keyword, n], [No.params, typeof t == "function" ? t(e) : t || de._`{}`]), c.messages) o.push([No.message, typeof r == "function" ? r(e) : r]);
    if (c.verbose) o.push([No.schema, s], [No.parentSchema, de._`${d}${p}`], [gt.default.data, i]);
    if (u) o.push([No.propertyName, u]);
  }
});
var wA = k((SA) => {
  Object.defineProperty(SA, "__esModule", { value: true });
  SA.boolOrEmptySchema = SA.topBoolOrEmptySchema = void 0;
  var TJ = Al(), PJ = re(), IJ = tn(), RJ = { message: "boolean schema is false" };
  function $J(e) {
    let { gen: t, schema: r, validateName: o } = e;
    if (r === false) vA(e, false);
    else if (typeof r == "object" && r.$async === true) t.return(IJ.default.data);
    else t.assign(PJ._`${o}.errors`, null), t.return(true);
  }
  SA.topBoolOrEmptySchema = $J;
  function OJ(e, t) {
    let { gen: r, schema: o } = e;
    if (o === false) r.var(t, false), vA(e);
    else r.var(t, true);
  }
  SA.boolOrEmptySchema = OJ;
  function vA(e, t) {
    let { gen: r, data: o } = e, n = { gen: r, keyword: "false schema", data: o, schema: false, schemaCode: false, schemaValue: false, params: {}, it: e };
    (0, TJ.reportError)(n, RJ, void 0, t);
  }
});
var OS = k((kA) => {
  Object.defineProperty(kA, "__esModule", { value: true });
  kA.getRules = kA.isJSONType = void 0;
  var CJ = ["string", "number", "integer", "boolean", "null", "object", "array"], MJ = new Set(CJ);
  function DJ(e) {
    return typeof e == "string" && MJ.has(e);
  }
  kA.isJSONType = DJ;
  function NJ() {
    let e = { number: { type: "number", rules: [] }, string: { type: "string", rules: [] }, array: { type: "array", rules: [] }, object: { type: "object", rules: [] } };
    return { types: { ...e, integer: true, boolean: true, null: true }, rules: [{ rules: [] }, e.number, e.string, e.array, e.object], post: { rules: [] }, all: {}, keywords: {} };
  }
  kA.getRules = NJ;
});
var AS = k((IA) => {
  Object.defineProperty(IA, "__esModule", { value: true });
  IA.shouldUseRule = IA.shouldUseGroup = IA.schemaHasRulesForType = void 0;
  function UJ({ schema: e, self: t }, r) {
    let o = t.RULES.types[r];
    return o && o !== true && TA(e, o);
  }
  IA.schemaHasRulesForType = UJ;
  function TA(e, t) {
    return t.rules.some((r) => PA(e, r));
  }
  IA.shouldUseGroup = TA;
  function PA(e, t) {
    var r;
    return e[t.keyword] !== void 0 || ((r = t.definition.implements) === null || r === void 0 ? void 0 : r.some((o) => e[o] !== void 0));
  }
  IA.shouldUseRule = PA;
});
var Cl = k((CA) => {
  Object.defineProperty(CA, "__esModule", { value: true });
  CA.reportTypeError = CA.checkDataTypes = CA.checkDataType = CA.coerceAndCheckDataType = CA.getJSONTypes = CA.getSchemaTypes = CA.DataType = void 0;
  var FJ = OS(), BJ = AS(), HJ = Al(), te = re(), $A = ue(), bs;
  (function(e) {
    e[e.Correct = 0] = "Correct", e[e.Wrong = 1] = "Wrong";
  })(bs || (CA.DataType = bs = {}));
  function qJ(e) {
    let t = OA(e.type);
    if (t.includes("null")) {
      if (e.nullable === false) throw Error("type: null contradicts nullable: false");
    } else {
      if (!t.length && e.nullable !== void 0) throw Error('"nullable" cannot be used without "type"');
      if (e.nullable === true) t.push("null");
    }
    return t;
  }
  CA.getSchemaTypes = qJ;
  function OA(e) {
    let t = Array.isArray(e) ? e : e ? [e] : [];
    if (t.every(FJ.isJSONType)) return t;
    throw Error("type must be JSONType or JSONType[]: " + t.join(","));
  }
  CA.getJSONTypes = OA;
  function ZJ(e, t) {
    let { gen: r, data: o, opts: n } = e, i = VJ(t, n.coerceTypes), s = t.length > 0 && !(i.length === 0 && t.length === 1 && (0, BJ.schemaHasRulesForType)(e, t[0]));
    if (s) {
      let a = MS(t, o, n.strictNumbers, bs.Wrong);
      r.if(a, () => {
        if (i.length) WJ(e, t, i);
        else DS(e);
      });
    }
    return s;
  }
  CA.coerceAndCheckDataType = ZJ;
  var AA = /* @__PURE__ */ new Set(["string", "number", "integer", "boolean", "null"]);
  function VJ(e, t) {
    return t ? e.filter((r) => AA.has(r) || t === "array" && r === "array") : [];
  }
  function WJ(e, t, r) {
    let { gen: o, data: n, opts: i } = e, s = o.let("dataType", te._`typeof ${n}`), a = o.let("coerced", te._`undefined`);
    if (i.coerceTypes === "array") o.if(te._`${s} == 'object' && Array.isArray(${n}) && ${n}.length == 1`, () => o.assign(n, te._`${n}[0]`).assign(s, te._`typeof ${n}`).if(MS(t, n, i.strictNumbers), () => o.assign(a, n)));
    o.if(te._`${a} !== undefined`);
    for (let u of r) if (AA.has(u) || u === "array" && i.coerceTypes === "array") c(u);
    o.else(), DS(e), o.endIf(), o.if(te._`${a} !== undefined`, () => {
      o.assign(n, a), KJ(e, a);
    });
    function c(u) {
      switch (u) {
        case "string":
          o.elseIf(te._`${s} == "number" || ${s} == "boolean"`).assign(a, te._`"" + ${n}`).elseIf(te._`${n} === null`).assign(a, te._`""`);
          return;
        case "number":
          o.elseIf(te._`${s} == "boolean" || ${n} === null
              || (${s} == "string" && ${n} && ${n} == +${n})`).assign(a, te._`+${n}`);
          return;
        case "integer":
          o.elseIf(te._`${s} === "boolean" || ${n} === null
              || (${s} === "string" && ${n} && ${n} == +${n} && !(${n} % 1))`).assign(a, te._`+${n}`);
          return;
        case "boolean":
          o.elseIf(te._`${n} === "false" || ${n} === 0 || ${n} === null`).assign(a, false).elseIf(te._`${n} === "true" || ${n} === 1`).assign(a, true);
          return;
        case "null":
          o.elseIf(te._`${n} === "" || ${n} === 0 || ${n} === false`), o.assign(a, null);
          return;
        case "array":
          o.elseIf(te._`${s} === "string" || ${s} === "number"
              || ${s} === "boolean" || ${n} === null`).assign(a, te._`[${n}]`);
      }
    }
  }
  function KJ({ gen: e, parentData: t, parentDataProperty: r }, o) {
    e.if(te._`${t} !== undefined`, () => e.assign(te._`${t}[${r}]`, o));
  }
  function CS(e, t, r, o = bs.Correct) {
    let n = o === bs.Correct ? te.operators.EQ : te.operators.NEQ, i;
    switch (e) {
      case "null":
        return te._`${t} ${n} null`;
      case "array":
        i = te._`Array.isArray(${t})`;
        break;
      case "object":
        i = te._`${t} && typeof ${t} == "object" && !Array.isArray(${t})`;
        break;
      case "integer":
        i = s(te._`!(${t} % 1) && !isNaN(${t})`);
        break;
      case "number":
        i = s();
        break;
      default:
        return te._`typeof ${t} ${n} ${e}`;
    }
    return o === bs.Correct ? i : (0, te.not)(i);
    function s(a = te.nil) {
      return (0, te.and)(te._`typeof ${t} == "number"`, a, r ? te._`isFinite(${t})` : te.nil);
    }
  }
  CA.checkDataType = CS;
  function MS(e, t, r, o) {
    if (e.length === 1) return CS(e[0], t, r, o);
    let n, i = (0, $A.toHash)(e);
    if (i.array && i.object) {
      let s = te._`typeof ${t} != "object"`;
      n = i.null ? s : te._`!${t} || ${s}`, delete i.null, delete i.array, delete i.object;
    } else n = te.nil;
    if (i.number) delete i.integer;
    for (let s in i) n = (0, te.and)(n, CS(s, t, r, o));
    return n;
  }
  CA.checkDataTypes = MS;
  var GJ = { message: ({ schema: e }) => `must be ${e}`, params: ({ schema: e, schemaValue: t }) => typeof e == "string" ? te._`{type: ${e}}` : te._`{type: ${t}}` };
  function DS(e) {
    let t = JJ(e);
    (0, HJ.reportError)(t, GJ);
  }
  CA.reportTypeError = DS;
  function JJ(e) {
    let { gen: t, data: r, schema: o } = e, n = (0, $A.schemaRefOrVal)(e, o, "type");
    return { gen: t, keyword: "type", data: r, schema: o.type, schemaCode: n, schemaValue: n, parentSchema: o, params: {}, it: e };
  }
});
var UA = k((NA) => {
  Object.defineProperty(NA, "__esModule", { value: true });
  NA.assignDefaults = void 0;
  var _s = re(), n5 = ue();
  function o5(e, t) {
    let { properties: r, items: o } = e.schema;
    if (t === "object" && r) for (let n in r) DA(e, n, r[n].default);
    else if (t === "array" && Array.isArray(o)) o.forEach((n, i) => DA(e, i, n.default));
  }
  NA.assignDefaults = o5;
  function DA(e, t, r) {
    let { gen: o, compositeRule: n, data: i, opts: s } = e;
    if (r === void 0) return;
    let a = _s._`${i}${(0, _s.getProperty)(t)}`;
    if (n) {
      (0, n5.checkStrictMode)(e, `default is ignored for: ${a}`);
      return;
    }
    let c = _s._`${a} === undefined`;
    if (s.useDefaults === "empty") c = _s._`${c} || ${a} === null || ${a} === ""`;
    o.if(c, _s._`${a} = ${(0, _s.stringify)(r)}`);
  }
});
var er = k((FA) => {
  Object.defineProperty(FA, "__esModule", { value: true });
  FA.validateUnion = FA.validateArray = FA.usePattern = FA.callValidateCode = FA.schemaProperties = FA.allSchemaProperties = FA.noPropertyInData = FA.propertyInData = FA.isOwnProperty = FA.hasPropFunc = FA.reportMissingProp = FA.checkMissingProp = FA.checkReportMissingProp = void 0;
  var Ae = re(), NS = ue(), Fn = tn(), i5 = ue();
  function s5(e, t) {
    let { gen: r, data: o, it: n } = e;
    r.if(US(r, o, t, n.opts.ownProperties), () => {
      e.setParams({ missingProperty: Ae._`${t}` }, true), e.error();
    });
  }
  FA.checkReportMissingProp = s5;
  function a5({ gen: e, data: t, it: { opts: r } }, o, n) {
    return (0, Ae.or)(...o.map((i) => (0, Ae.and)(US(e, t, i, r.ownProperties), Ae._`${n} = ${i}`)));
  }
  FA.checkMissingProp = a5;
  function c5(e, t) {
    e.setParams({ missingProperty: t }, true), e.error();
  }
  FA.reportMissingProp = c5;
  function zA(e) {
    return e.scopeValue("func", { ref: Object.prototype.hasOwnProperty, code: Ae._`Object.prototype.hasOwnProperty` });
  }
  FA.hasPropFunc = zA;
  function jS(e, t, r) {
    return Ae._`${zA(e)}.call(${t}, ${r})`;
  }
  FA.isOwnProperty = jS;
  function l5(e, t, r, o) {
    let n = Ae._`${t}${(0, Ae.getProperty)(r)} !== undefined`;
    return o ? Ae._`${n} && ${jS(e, t, r)}` : n;
  }
  FA.propertyInData = l5;
  function US(e, t, r, o) {
    let n = Ae._`${t}${(0, Ae.getProperty)(r)} === undefined`;
    return o ? (0, Ae.or)(n, (0, Ae.not)(jS(e, t, r))) : n;
  }
  FA.noPropertyInData = US;
  function LA(e) {
    return e ? Object.keys(e).filter((t) => t !== "__proto__") : [];
  }
  FA.allSchemaProperties = LA;
  function u5(e, t) {
    return LA(t).filter((r) => !(0, NS.alwaysValidSchema)(e, t[r]));
  }
  FA.schemaProperties = u5;
  function d5({ schemaCode: e, data: t, it: { gen: r, topSchemaRef: o, schemaPath: n, errorPath: i }, it: s }, a, c, u) {
    let d = u ? Ae._`${e}, ${t}, ${o}${n}` : t, p = [[Fn.default.instancePath, (0, Ae.strConcat)(Fn.default.instancePath, i)], [Fn.default.parentData, s.parentData], [Fn.default.parentDataProperty, s.parentDataProperty], [Fn.default.rootData, Fn.default.rootData]];
    if (s.opts.dynamicRef) p.push([Fn.default.dynamicAnchors, Fn.default.dynamicAnchors]);
    let f = Ae._`${d}, ${r.object(...p)}`;
    return c !== Ae.nil ? Ae._`${a}.call(${c}, ${f})` : Ae._`${a}(${f})`;
  }
  FA.callValidateCode = d5;
  var p5 = Ae._`new RegExp`;
  function f5({ gen: e, it: { opts: t } }, r) {
    let o = t.unicodeRegExp ? "u" : "", { regExp: n } = t.code, i = n(r, o);
    return e.scopeValue("pattern", { key: i.toString(), ref: i, code: Ae._`${n.code === "new RegExp" ? p5 : (0, i5.useFunc)(e, n)}(${r}, ${o})` });
  }
  FA.usePattern = f5;
  function m5(e) {
    let { gen: t, data: r, keyword: o, it: n } = e, i = t.name("valid");
    if (n.allErrors) {
      let a = t.let("valid", true);
      return s(() => t.assign(a, false)), a;
    }
    return t.var(i, true), s(() => t.break()), i;
    function s(a) {
      let c = t.const("len", Ae._`${r}.length`);
      t.forRange("i", 0, c, (u) => {
        e.subschema({ keyword: o, dataProp: u, dataPropType: NS.Type.Num }, i), t.if((0, Ae.not)(i), a);
      });
    }
  }
  FA.validateArray = m5;
  function g5(e) {
    let { gen: t, schema: r, keyword: o, it: n } = e;
    if (!Array.isArray(r)) throw Error("ajv implementation error");
    if (r.some((c) => (0, NS.alwaysValidSchema)(n, c)) && !n.opts.unevaluated) return;
    let s = t.let("valid", false), a = t.name("_valid");
    t.block(() => r.forEach((c, u) => {
      let d = e.subschema({ keyword: o, schemaProp: u, compositeRule: true }, a);
      if (t.assign(s, Ae._`${s} || ${a}`), !e.mergeValidEvaluated(d, a)) t.if((0, Ae.not)(s));
    })), e.result(s, () => e.reset(), () => e.error(true));
  }
  FA.validateUnion = g5;
});
var WA = k((ZA) => {
  Object.defineProperty(ZA, "__esModule", { value: true });
  ZA.validateKeywordUsage = ZA.validSchemaType = ZA.funcKeywordCode = ZA.macroKeywordCode = void 0;
  var ht = re(), jo = tn(), I5 = er(), R5 = Al();
  function $5(e, t) {
    let { gen: r, keyword: o, schema: n, parentSchema: i, it: s } = e, a = t.macro.call(s.self, n, i, s), c = qA(r, o, a);
    if (s.opts.validateSchema !== false) s.self.validateSchema(a, true);
    let u = r.name("valid");
    e.subschema({ schema: a, schemaPath: ht.nil, errSchemaPath: `${s.errSchemaPath}/${o}`, topSchemaRef: c, compositeRule: true }, u), e.pass(u, () => e.error(true));
  }
  ZA.macroKeywordCode = $5;
  function O5(e, t) {
    var r;
    let { gen: o, keyword: n, schema: i, parentSchema: s, $data: a, it: c } = e;
    C5(c, t);
    let u = !a && t.compile ? t.compile.call(c.self, i, s, c) : t.validate, d = qA(o, n, u), p = o.let("valid");
    e.block$data(p, f), e.ok((r = t.valid) !== null && r !== void 0 ? r : p);
    function f() {
      if (t.errors === false) {
        if (h(), t.modifying) HA(e);
        y(() => e.error());
      } else {
        let v = t.async ? m() : g();
        if (t.modifying) HA(e);
        y(() => A5(e, v));
      }
    }
    function m() {
      let v = o.let("ruleErrs", null);
      return o.try(() => h(ht._`await `), (x) => o.assign(p, false).if(ht._`${x} instanceof ${c.ValidationError}`, () => o.assign(v, ht._`${x}.errors`), () => o.throw(x))), v;
    }
    function g() {
      let v = ht._`${d}.errors`;
      return o.assign(v, null), h(ht.nil), v;
    }
    function h(v = t.async ? ht._`await ` : ht.nil) {
      let x = c.opts.passContext ? jo.default.this : jo.default.self, w = !("compile" in t && !a || t.schema === false);
      o.assign(p, ht._`${v}${(0, I5.callValidateCode)(e, d, x, w)}`, t.modifying);
    }
    function y(v) {
      var x;
      o.if((0, ht.not)((x = t.valid) !== null && x !== void 0 ? x : p), v);
    }
  }
  ZA.funcKeywordCode = O5;
  function HA(e) {
    let { gen: t, data: r, it: o } = e;
    t.if(o.parentData, () => t.assign(r, ht._`${o.parentData}[${o.parentDataProperty}]`));
  }
  function A5(e, t) {
    let { gen: r } = e;
    r.if(ht._`Array.isArray(${t})`, () => {
      r.assign(jo.default.vErrors, ht._`${jo.default.vErrors} === null ? ${t} : ${jo.default.vErrors}.concat(${t})`).assign(jo.default.errors, ht._`${jo.default.vErrors}.length`), (0, R5.extendErrors)(e);
    }, () => e.error());
  }
  function C5({ schemaEnv: e }, t) {
    if (t.async && !e.$async) throw Error("async keyword in sync schema");
  }
  function qA(e, t, r) {
    if (r === void 0) throw Error(`keyword "${t}" failed to compile`);
    return e.scopeValue("keyword", typeof r == "function" ? { ref: r } : { ref: r, code: (0, ht.stringify)(r) });
  }
  function M5(e, t, r = false) {
    return !t.length || t.some((o) => o === "array" ? Array.isArray(e) : o === "object" ? e && typeof e == "object" && !Array.isArray(e) : typeof e == o || r && typeof e > "u");
  }
  ZA.validSchemaType = M5;
  function D5({ schema: e, opts: t, self: r, errSchemaPath: o }, n, i) {
    if (Array.isArray(n.keyword) ? !n.keyword.includes(i) : n.keyword !== i) throw Error("ajv implementation error");
    let s = n.dependencies;
    if (s === null || s === void 0 ? void 0 : s.some((a) => !Object.prototype.hasOwnProperty.call(e, a))) throw Error(`parent schema must have dependencies of ${i}: ${s.join(",")}`);
    if (n.validateSchema) {
      if (!n.validateSchema(e[i])) {
        let c = `keyword "${i}" value is invalid at path "${o}": ` + r.errorsText(n.validateSchema.errors);
        if (t.validateSchema === "log") r.logger.error(c);
        else throw Error(c);
      }
    }
  }
  ZA.validateKeywordUsage = D5;
});
var XA = k((GA) => {
  Object.defineProperty(GA, "__esModule", { value: true });
  GA.extendSubschemaMode = GA.extendSubschemaData = GA.getSubschema = void 0;
  var Pr = re(), KA = ue();
  function z5(e, { keyword: t, schemaProp: r, schema: o, schemaPath: n, errSchemaPath: i, topSchemaRef: s }) {
    if (t !== void 0 && o !== void 0) throw Error('both "keyword" and "schema" passed, only one allowed');
    if (t !== void 0) {
      let a = e.schema[t];
      return r === void 0 ? { schema: a, schemaPath: Pr._`${e.schemaPath}${(0, Pr.getProperty)(t)}`, errSchemaPath: `${e.errSchemaPath}/${t}` } : { schema: a[r], schemaPath: Pr._`${e.schemaPath}${(0, Pr.getProperty)(t)}${(0, Pr.getProperty)(r)}`, errSchemaPath: `${e.errSchemaPath}/${t}/${(0, KA.escapeFragment)(r)}` };
    }
    if (o !== void 0) {
      if (n === void 0 || i === void 0 || s === void 0) throw Error('"schemaPath", "errSchemaPath" and "topSchemaRef" are required with "schema"');
      return { schema: o, schemaPath: n, topSchemaRef: s, errSchemaPath: i };
    }
    throw Error('either "keyword" or "schema" must be passed');
  }
  GA.getSubschema = z5;
  function L5(e, t, { dataProp: r, dataPropType: o, data: n, dataTypes: i, propertyName: s }) {
    if (n !== void 0 && r !== void 0) throw Error('both "data" and "dataProp" passed, only one allowed');
    let { gen: a } = t;
    if (r !== void 0) {
      let { errorPath: u, dataPathArr: d, opts: p } = t, f = a.let("data", Pr._`${t.data}${(0, Pr.getProperty)(r)}`, true);
      c(f), e.errorPath = Pr.str`${u}${(0, KA.getErrorPath)(r, o, p.jsPropertySyntax)}`, e.parentDataProperty = Pr._`${r}`, e.dataPathArr = [...d, e.parentDataProperty];
    }
    if (n !== void 0) {
      let u = n instanceof Pr.Name ? n : a.let("data", n, true);
      if (c(u), s !== void 0) e.propertyName = s;
    }
    if (i) e.dataTypes = i;
    function c(u) {
      e.data = u, e.dataLevel = t.dataLevel + 1, e.dataTypes = [], t.definedProperties = /* @__PURE__ */ new Set(), e.parentData = t.data, e.dataNames = [...t.dataNames, u];
    }
  }
  GA.extendSubschemaData = L5;
  function F5(e, { jtdDiscriminator: t, jtdMetadata: r, compositeRule: o, createErrors: n, allErrors: i }) {
    if (o !== void 0) e.compositeRule = o;
    if (n !== void 0) e.createErrors = n;
    if (i !== void 0) e.allErrors = i;
    e.jtdDiscriminator = t, e.jtdMetadata = r;
  }
  GA.extendSubschemaMode = F5;
});
var zS = k(($Ee, YA) => {
  YA.exports = function e(t, r) {
    if (t === r) return true;
    if (t && r && typeof t == "object" && typeof r == "object") {
      if (t.constructor !== r.constructor) return false;
      var o, n, i;
      if (Array.isArray(t)) {
        if (o = t.length, o != r.length) return false;
        for (n = o; n-- !== 0; ) if (!e(t[n], r[n])) return false;
        return true;
      }
      if (t.constructor === RegExp) return t.source === r.source && t.flags === r.flags;
      if (t.valueOf !== Object.prototype.valueOf) return t.valueOf() === r.valueOf();
      if (t.toString !== Object.prototype.toString) return t.toString() === r.toString();
      if (i = Object.keys(t), o = i.length, o !== Object.keys(r).length) return false;
      for (n = o; n-- !== 0; ) if (!Object.prototype.hasOwnProperty.call(r, i[n])) return false;
      for (n = o; n-- !== 0; ) {
        var s = i[n];
        if (!e(t[s], r[s])) return false;
      }
      return true;
    }
    return t !== t && r !== r;
  };
});
var eC = k((OEe, QA) => {
  var Bn = QA.exports = function(e, t, r) {
    if (typeof t == "function") r = t, t = {};
    r = t.cb || r;
    var o = typeof r == "function" ? r : r.pre || function() {
    }, n = r.post || function() {
    };
    Am(t, o, n, e, "", e);
  };
  Bn.keywords = { additionalItems: true, items: true, contains: true, additionalProperties: true, propertyNames: true, not: true, if: true, then: true, else: true };
  Bn.arrayKeywords = { items: true, allOf: true, anyOf: true, oneOf: true };
  Bn.propsKeywords = { $defs: true, definitions: true, properties: true, patternProperties: true, dependencies: true };
  Bn.skipKeywords = { default: true, enum: true, const: true, required: true, maximum: true, minimum: true, exclusiveMaximum: true, exclusiveMinimum: true, multipleOf: true, maxLength: true, minLength: true, pattern: true, format: true, maxItems: true, minItems: true, uniqueItems: true, maxProperties: true, minProperties: true };
  function Am(e, t, r, o, n, i, s, a, c, u) {
    if (o && typeof o == "object" && !Array.isArray(o)) {
      t(o, n, i, s, a, c, u);
      for (var d in o) {
        var p = o[d];
        if (Array.isArray(p)) {
          if (d in Bn.arrayKeywords) for (var f = 0; f < p.length; f++) Am(e, t, r, p[f], n + "/" + d + "/" + f, i, n, d, o, f);
        } else if (d in Bn.propsKeywords) {
          if (p && typeof p == "object") for (var m in p) Am(e, t, r, p[m], n + "/" + d + "/" + q5(m), i, n, d, o, m);
        } else if (d in Bn.keywords || e.allKeys && !(d in Bn.skipKeywords)) Am(e, t, r, p, n + "/" + d, i, n, d, o);
      }
      r(o, n, i, s, a, c, u);
    }
  }
  function q5(e) {
    return e.replace(/~/g, "~0").replace(/\//g, "~1");
  }
});
var Ml = k((oC) => {
  Object.defineProperty(oC, "__esModule", { value: true });
  oC.getSchemaRefs = oC.resolveUrl = oC.normalizeId = oC._getFullPath = oC.getFullPath = oC.inlineRef = void 0;
  var Z5 = ue(), V5 = zS(), W5 = eC(), K5 = /* @__PURE__ */ new Set(["type", "format", "pattern", "maxLength", "minLength", "maxProperties", "minProperties", "maxItems", "minItems", "maximum", "minimum", "uniqueItems", "multipleOf", "required", "enum", "const"]);
  function G5(e, t = true) {
    if (typeof e == "boolean") return true;
    if (t === true) return !LS(e);
    if (!t) return false;
    return tC(e) <= t;
  }
  oC.inlineRef = G5;
  var J5 = /* @__PURE__ */ new Set(["$ref", "$recursiveRef", "$recursiveAnchor", "$dynamicRef", "$dynamicAnchor"]);
  function LS(e) {
    for (let t in e) {
      if (J5.has(t)) return true;
      let r = e[t];
      if (Array.isArray(r) && r.some(LS)) return true;
      if (typeof r == "object" && LS(r)) return true;
    }
    return false;
  }
  function tC(e) {
    let t = 0;
    for (let r in e) {
      if (r === "$ref") return 1 / 0;
      if (t++, K5.has(r)) continue;
      if (typeof e[r] == "object") (0, Z5.eachItem)(e[r], (o) => t += tC(o));
      if (t === 1 / 0) return 1 / 0;
    }
    return t;
  }
  function rC(e, t = "", r) {
    if (r !== false) t = vs(t);
    let o = e.parse(t);
    return nC(e, o);
  }
  oC.getFullPath = rC;
  function nC(e, t) {
    return e.serialize(t).split("#")[0] + "#";
  }
  oC._getFullPath = nC;
  var X5 = /#\/?$/;
  function vs(e) {
    return e ? e.replace(X5, "") : "";
  }
  oC.normalizeId = vs;
  function Y5(e, t, r) {
    return r = vs(r), e.resolve(t, r);
  }
  oC.resolveUrl = Y5;
  var Q5 = /^[a-z_][-a-z0-9._]*$/i;
  function e3(e, t) {
    if (typeof e == "boolean") return {};
    let { schemaId: r, uriResolver: o } = this.opts, n = vs(e[r] || t), i = { "": n }, s = rC(o, n, false), a = {}, c = /* @__PURE__ */ new Set();
    return W5(e, { allKeys: true }, (p, f, m, g) => {
      if (g === void 0) return;
      let h = s + f, y = i[g];
      if (typeof p[r] == "string") y = v.call(this, p[r]);
      x.call(this, p.$anchor), x.call(this, p.$dynamicAnchor), i[f] = y;
      function v(w) {
        let A = this.opts.uriResolver.resolve;
        if (w = vs(y ? A(y, w) : w), c.has(w)) throw d(w);
        c.add(w);
        let U = this.refs[w];
        if (typeof U == "string") U = this.refs[U];
        if (typeof U == "object") u(p, U.schema, w);
        else if (w !== vs(h)) if (w[0] === "#") u(p, a[w], w), a[w] = p;
        else this.refs[w] = h;
        return w;
      }
      function x(w) {
        if (typeof w == "string") {
          if (!Q5.test(w)) throw Error(`invalid anchor "${w}"`);
          v.call(this, `#${w}`);
        }
      }
    }), a;
    function u(p, f, m) {
      if (f !== void 0 && !V5(p, f)) throw d(m);
    }
    function d(p) {
      return Error(`reference "${p}" resolves to more than one schema`);
    }
  }
  oC.getSchemaRefs = e3;
});
var jl = k((vC) => {
  Object.defineProperty(vC, "__esModule", { value: true });
  vC.getData = vC.KeywordCxt = vC.validateFunctionCode = void 0;
  var uC = wA(), sC = Cl(), BS = AS(), Cm = Cl(), s3 = UA(), Nl = WA(), FS = XA(), q = re(), X = tn(), a3 = Ml(), rn = ue(), Dl = Al();
  function c3(e) {
    if (fC(e)) {
      if (mC(e), pC(e)) {
        d3(e);
        return;
      }
    }
    dC(e, () => (0, uC.topBoolOrEmptySchema)(e));
  }
  vC.validateFunctionCode = c3;
  function dC({ gen: e, validateName: t, schema: r, schemaEnv: o, opts: n }, i) {
    if (n.code.es5) e.func(t, q._`${X.default.data}, ${X.default.valCxt}`, o.$async, () => {
      e.code(q._`"use strict"; ${aC(r, n)}`), u3(e, n), e.code(i);
    });
    else e.func(t, q._`${X.default.data}, ${l3(n)}`, o.$async, () => e.code(aC(r, n)).code(i));
  }
  function l3(e) {
    return q._`{${X.default.instancePath}="", ${X.default.parentData}, ${X.default.parentDataProperty}, ${X.default.rootData}=${X.default.data}${e.dynamicRef ? q._`, ${X.default.dynamicAnchors}={}` : q.nil}}={}`;
  }
  function u3(e, t) {
    e.if(X.default.valCxt, () => {
      if (e.var(X.default.instancePath, q._`${X.default.valCxt}.${X.default.instancePath}`), e.var(X.default.parentData, q._`${X.default.valCxt}.${X.default.parentData}`), e.var(X.default.parentDataProperty, q._`${X.default.valCxt}.${X.default.parentDataProperty}`), e.var(X.default.rootData, q._`${X.default.valCxt}.${X.default.rootData}`), t.dynamicRef) e.var(X.default.dynamicAnchors, q._`${X.default.valCxt}.${X.default.dynamicAnchors}`);
    }, () => {
      if (e.var(X.default.instancePath, q._`""`), e.var(X.default.parentData, q._`undefined`), e.var(X.default.parentDataProperty, q._`undefined`), e.var(X.default.rootData, X.default.data), t.dynamicRef) e.var(X.default.dynamicAnchors, q._`{}`);
    });
  }
  function d3(e) {
    let { schema: t, opts: r, gen: o } = e;
    dC(e, () => {
      if (r.$comment && t.$comment) hC(e);
      if (h3(e), o.let(X.default.vErrors, null), o.let(X.default.errors, 0), r.unevaluated) p3(e);
      gC(e), _3(e);
    });
    return;
  }
  function p3(e) {
    let { gen: t, validateName: r } = e;
    e.evaluated = t.const("evaluated", q._`${r}.evaluated`), t.if(q._`${e.evaluated}.dynamicProps`, () => t.assign(q._`${e.evaluated}.props`, q._`undefined`)), t.if(q._`${e.evaluated}.dynamicItems`, () => t.assign(q._`${e.evaluated}.items`, q._`undefined`));
  }
  function aC(e, t) {
    let r = typeof e == "object" && e[t.schemaId];
    return r && (t.code.source || t.code.process) ? q._`/*# sourceURL=${r} */` : q.nil;
  }
  function f3(e, t) {
    if (fC(e)) {
      if (mC(e), pC(e)) {
        m3(e, t);
        return;
      }
    }
    (0, uC.boolOrEmptySchema)(e, t);
  }
  function pC({ schema: e, self: t }) {
    if (typeof e == "boolean") return !e;
    for (let r in e) if (t.RULES.all[r]) return true;
    return false;
  }
  function fC(e) {
    return typeof e.schema != "boolean";
  }
  function m3(e, t) {
    let { schema: r, gen: o, opts: n } = e;
    if (n.$comment && r.$comment) hC(e);
    y3(e), b3(e);
    let i = o.const("_errs", X.default.errors);
    gC(e, i), o.var(t, q._`${i} === ${X.default.errors}`);
  }
  function mC(e) {
    (0, rn.checkUnknownRules)(e), g3(e);
  }
  function gC(e, t) {
    if (e.opts.jtd) return cC(e, [], false, t);
    let r = (0, sC.getSchemaTypes)(e.schema), o = (0, sC.coerceAndCheckDataType)(e, r);
    cC(e, r, !o, t);
  }
  function g3(e) {
    let { schema: t, errSchemaPath: r, opts: o, self: n } = e;
    if (t.$ref && o.ignoreKeywordsWithRef && (0, rn.schemaHasRulesButRef)(t, n.RULES)) n.logger.warn(`$ref: keywords ignored in schema at path "${r}"`);
  }
  function h3(e) {
    let { schema: t, opts: r } = e;
    if (t.default !== void 0 && r.useDefaults && r.strictSchema) (0, rn.checkStrictMode)(e, "default is ignored in the schema root");
  }
  function y3(e) {
    let t = e.schema[e.opts.schemaId];
    if (t) e.baseId = (0, a3.resolveUrl)(e.opts.uriResolver, e.baseId, t);
  }
  function b3(e) {
    if (e.schema.$async && !e.schemaEnv.$async) throw Error("async schema in sync schema");
  }
  function hC({ gen: e, schemaEnv: t, schema: r, errSchemaPath: o, opts: n }) {
    let i = r.$comment;
    if (n.$comment === true) e.code(q._`${X.default.self}.logger.log(${i})`);
    else if (typeof n.$comment == "function") {
      let s = q.str`${o}/$comment`, a = e.scopeValue("root", { ref: t.root });
      e.code(q._`${X.default.self}.opts.$comment(${i}, ${s}, ${a}.schema)`);
    }
  }
  function _3(e) {
    let { gen: t, schemaEnv: r, validateName: o, ValidationError: n, opts: i } = e;
    if (r.$async) t.if(q._`${X.default.errors} === 0`, () => t.return(X.default.data), () => t.throw(q._`new ${n}(${X.default.vErrors})`));
    else {
      if (t.assign(q._`${o}.errors`, X.default.vErrors), i.unevaluated) v3(e);
      t.return(q._`${X.default.errors} === 0`);
    }
  }
  function v3({ gen: e, evaluated: t, props: r, items: o }) {
    if (r instanceof q.Name) e.assign(q._`${t}.props`, r);
    if (o instanceof q.Name) e.assign(q._`${t}.items`, o);
  }
  function cC(e, t, r, o) {
    let { gen: n, schema: i, data: s, allErrors: a, opts: c, self: u } = e, { RULES: d } = u;
    if (i.$ref && (c.ignoreKeywordsWithRef || !(0, rn.schemaHasRulesButRef)(i, d))) {
      n.block(() => bC(e, "$ref", d.all.$ref.definition));
      return;
    }
    if (!c.jtd) S3(e, t);
    n.block(() => {
      for (let f of d.rules) p(f);
      p(d.post);
    });
    function p(f) {
      if (!(0, BS.shouldUseGroup)(i, f)) return;
      if (f.type) {
        if (n.if((0, Cm.checkDataType)(f.type, s, c.strictNumbers)), lC(e, f), t.length === 1 && t[0] === f.type && r) n.else(), (0, Cm.reportTypeError)(e);
        n.endIf();
      } else lC(e, f);
      if (!a) n.if(q._`${X.default.errors} === ${o || 0}`);
    }
  }
  function lC(e, t) {
    let { gen: r, schema: o, opts: { useDefaults: n } } = e;
    if (n) (0, s3.assignDefaults)(e, t.type);
    r.block(() => {
      for (let i of t.rules) if ((0, BS.shouldUseRule)(o, i)) bC(e, i.keyword, i.definition, t.type);
    });
  }
  function S3(e, t) {
    if (e.schemaEnv.meta || !e.opts.strictTypes) return;
    if (x3(e, t), !e.opts.allowUnionTypes) w3(e, t);
    k3(e, e.dataTypes);
  }
  function x3(e, t) {
    if (!t.length) return;
    if (!e.dataTypes.length) {
      e.dataTypes = t;
      return;
    }
    t.forEach((r) => {
      if (!yC(e.dataTypes, r)) HS(e, `type "${r}" not allowed by context "${e.dataTypes.join(",")}"`);
    }), T3(e, t);
  }
  function w3(e, t) {
    if (t.length > 1 && !(t.length === 2 && t.includes("null"))) HS(e, "use allowUnionTypes to allow union type keyword");
  }
  function k3(e, t) {
    let r = e.self.RULES.all;
    for (let o in r) {
      let n = r[o];
      if (typeof n == "object" && (0, BS.shouldUseRule)(e.schema, n)) {
        let { type: i } = n.definition;
        if (i.length && !i.some((s) => E3(t, s))) HS(e, `missing type "${i.join(",")}" for keyword "${o}"`);
      }
    }
  }
  function E3(e, t) {
    return e.includes(t) || t === "number" && e.includes("integer");
  }
  function yC(e, t) {
    return e.includes(t) || t === "integer" && e.includes("number");
  }
  function T3(e, t) {
    let r = [];
    for (let o of e.dataTypes) if (yC(t, o)) r.push(o);
    else if (t.includes("integer") && o === "number") r.push("integer");
    e.dataTypes = r;
  }
  function HS(e, t) {
    let r = e.schemaEnv.baseId + e.errSchemaPath;
    t += ` at "${r}" (strictTypes)`, (0, rn.checkStrictMode)(e, t, e.opts.strictTypes);
  }
  class qS {
    constructor(e, t, r) {
      if ((0, Nl.validateKeywordUsage)(e, t, r), this.gen = e.gen, this.allErrors = e.allErrors, this.keyword = r, this.data = e.data, this.schema = e.schema[r], this.$data = t.$data && e.opts.$data && this.schema && this.schema.$data, this.schemaValue = (0, rn.schemaRefOrVal)(e, this.schema, r, this.$data), this.schemaType = t.schemaType, this.parentSchema = e.schema, this.params = {}, this.it = e, this.def = t, this.$data) this.schemaCode = e.gen.const("vSchema", _C(this.$data, e));
      else if (this.schemaCode = this.schemaValue, !(0, Nl.validSchemaType)(this.schema, t.schemaType, t.allowUndefined)) throw Error(`${r} value must be ${JSON.stringify(t.schemaType)}`);
      if ("code" in t ? t.trackErrors : t.errors !== false) this.errsCount = e.gen.const("_errs", X.default.errors);
    }
    result(e, t, r) {
      this.failResult((0, q.not)(e), t, r);
    }
    failResult(e, t, r) {
      if (this.gen.if(e), r) r();
      else this.error();
      if (t) {
        if (this.gen.else(), t(), this.allErrors) this.gen.endIf();
      } else if (this.allErrors) this.gen.endIf();
      else this.gen.else();
    }
    pass(e, t) {
      this.failResult((0, q.not)(e), void 0, t);
    }
    fail(e) {
      if (e === void 0) {
        if (this.error(), !this.allErrors) this.gen.if(false);
        return;
      }
      if (this.gen.if(e), this.error(), this.allErrors) this.gen.endIf();
      else this.gen.else();
    }
    fail$data(e) {
      if (!this.$data) return this.fail(e);
      let { schemaCode: t } = this;
      this.fail(q._`${t} !== undefined && (${(0, q.or)(this.invalid$data(), e)})`);
    }
    error(e, t, r) {
      if (t) {
        this.setParams(t), this._error(e, r), this.setParams({});
        return;
      }
      this._error(e, r);
    }
    _error(e, t) {
      (e ? Dl.reportExtraError : Dl.reportError)(this, this.def.error, t);
    }
    $dataError() {
      (0, Dl.reportError)(this, this.def.$dataError || Dl.keyword$DataError);
    }
    reset() {
      if (this.errsCount === void 0) throw Error('add "trackErrors" to keyword definition');
      (0, Dl.resetErrorsCount)(this.gen, this.errsCount);
    }
    ok(e) {
      if (!this.allErrors) this.gen.if(e);
    }
    setParams(e, t) {
      if (t) Object.assign(this.params, e);
      else this.params = e;
    }
    block$data(e, t, r = q.nil) {
      this.gen.block(() => {
        this.check$data(e, r), t();
      });
    }
    check$data(e = q.nil, t = q.nil) {
      if (!this.$data) return;
      let { gen: r, schemaCode: o, schemaType: n, def: i } = this;
      if (r.if((0, q.or)(q._`${o} === undefined`, t)), e !== q.nil) r.assign(e, true);
      if (n.length || i.validateSchema) {
        if (r.elseIf(this.invalid$data()), this.$dataError(), e !== q.nil) r.assign(e, false);
      }
      r.else();
    }
    invalid$data() {
      let { gen: e, schemaCode: t, schemaType: r, def: o, it: n } = this;
      return (0, q.or)(i(), s());
      function i() {
        if (r.length) {
          if (!(t instanceof q.Name)) throw Error("ajv implementation error");
          let a = Array.isArray(r) ? r : [r];
          return q._`${(0, Cm.checkDataTypes)(a, t, n.opts.strictNumbers, Cm.DataType.Wrong)}`;
        }
        return q.nil;
      }
      function s() {
        if (o.validateSchema) {
          let a = e.scopeValue("validate$data", { ref: o.validateSchema });
          return q._`!${a}(${t})`;
        }
        return q.nil;
      }
    }
    subschema(e, t) {
      let r = (0, FS.getSubschema)(this.it, e);
      (0, FS.extendSubschemaData)(r, this.it, e), (0, FS.extendSubschemaMode)(r, e);
      let o = { ...this.it, ...r, items: void 0, props: void 0 };
      return f3(o, t), o;
    }
    mergeEvaluated(e, t) {
      let { it: r, gen: o } = this;
      if (!r.opts.unevaluated) return;
      if (r.props !== true && e.props !== void 0) r.props = rn.mergeEvaluated.props(o, e.props, r.props, t);
      if (r.items !== true && e.items !== void 0) r.items = rn.mergeEvaluated.items(o, e.items, r.items, t);
    }
    mergeValidEvaluated(e, t) {
      let { it: r, gen: o } = this;
      if (r.opts.unevaluated && (r.props !== true || r.items !== true)) return o.if(t, () => this.mergeEvaluated(e, q.Name)), true;
    }
  }
  vC.KeywordCxt = qS;
  function bC(e, t, r, o) {
    let n = new qS(e, r, t);
    if ("code" in r) r.code(n, o);
    else if (n.$data && r.validate) (0, Nl.funcKeywordCode)(n, r);
    else if ("macro" in r) (0, Nl.macroKeywordCode)(n, r);
    else if (r.compile || r.validate) (0, Nl.funcKeywordCode)(n, r);
  }
  var P3 = /^\/(?:[^~]|~0|~1)*$/, I3 = /^([0-9]+)(#|\/(?:[^~]|~0|~1)*)?$/;
  function _C(e, { dataLevel: t, dataNames: r, dataPathArr: o }) {
    let n, i;
    if (e === "") return X.default.rootData;
    if (e[0] === "/") {
      if (!P3.test(e)) throw Error(`Invalid JSON-pointer: ${e}`);
      n = e, i = X.default.rootData;
    } else {
      let u = I3.exec(e);
      if (!u) throw Error(`Invalid JSON-pointer: ${e}`);
      let d = +u[1];
      if (n = u[2], n === "#") {
        if (d >= t) throw Error(c("property/index", d));
        return o[t - d];
      }
      if (d > t) throw Error(c("data", d));
      if (i = r[t - d], !n) return i;
    }
    let s = i, a = n.split("/");
    for (let u of a) if (u) i = q._`${i}${(0, q.getProperty)((0, rn.unescapeJsonPointer)(u))}`, s = q._`${s} && ${i}`;
    return s;
    function c(u, d) {
      return `Cannot access ${u} ${d} levels up, current level is ${t}`;
    }
  }
  vC.getData = _C;
});
var Mm = k((wC) => {
  Object.defineProperty(wC, "__esModule", { value: true });
  class xC extends Error {
    constructor(e) {
      super("validation failed");
      this.errors = e, this.ajv = this.validation = true;
    }
  }
  wC.default = xC;
});
var Ul = k((EC) => {
  Object.defineProperty(EC, "__esModule", { value: true });
  var ZS = Ml();
  class kC extends Error {
    constructor(e, t, r, o) {
      super(o || `can't resolve reference ${r} from id ${t}`);
      this.missingRef = (0, ZS.resolveUrl)(e, t, r), this.missingSchema = (0, ZS.normalizeId)((0, ZS.getFullPath)(e, this.missingRef));
    }
  }
  EC.default = kC;
});
var Nm = k((IC) => {
  Object.defineProperty(IC, "__esModule", { value: true });
  IC.resolveSchema = IC.getCompilingSchema = IC.resolveRef = IC.compileSchema = IC.SchemaEnv = void 0;
  var gr = re(), C3 = Mm(), Uo = tn(), hr = Ml(), TC = ue(), M3 = jl();
  class zl {
    constructor(e) {
      var t;
      this.refs = {}, this.dynamicAnchors = {};
      let r;
      if (typeof e.schema == "object") r = e.schema;
      this.schema = e.schema, this.schemaId = e.schemaId, this.root = e.root || this, this.baseId = (t = e.baseId) !== null && t !== void 0 ? t : (0, hr.normalizeId)(r === null || r === void 0 ? void 0 : r[e.schemaId || "$id"]), this.schemaPath = e.schemaPath, this.localRefs = e.localRefs, this.meta = e.meta, this.$async = r === null || r === void 0 ? void 0 : r.$async, this.refs = {};
    }
  }
  IC.SchemaEnv = zl;
  function WS(e) {
    let t = PC.call(this, e);
    if (t) return t;
    let r = (0, hr.getFullPath)(this.opts.uriResolver, e.root.baseId), { es5: o, lines: n } = this.opts.code, { ownProperties: i } = this.opts, s = new gr.CodeGen(this.scope, { es5: o, lines: n, ownProperties: i }), a;
    if (e.$async) a = s.scopeValue("Error", { ref: C3.default, code: gr._`require("ajv/dist/runtime/validation_error").default` });
    let c = s.scopeName("validate");
    e.validateName = c;
    let u = { gen: s, allErrors: this.opts.allErrors, data: Uo.default.data, parentData: Uo.default.parentData, parentDataProperty: Uo.default.parentDataProperty, dataNames: [Uo.default.data], dataPathArr: [gr.nil], dataLevel: 0, dataTypes: [], definedProperties: /* @__PURE__ */ new Set(), topSchemaRef: s.scopeValue("schema", this.opts.code.source === true ? { ref: e.schema, code: (0, gr.stringify)(e.schema) } : { ref: e.schema }), validateName: c, ValidationError: a, schema: e.schema, schemaEnv: e, rootId: r, baseId: e.baseId || r, schemaPath: gr.nil, errSchemaPath: e.schemaPath || (this.opts.jtd ? "" : "#"), errorPath: gr._`""`, opts: this.opts, self: this }, d;
    try {
      this._compilations.add(e), (0, M3.validateFunctionCode)(u), s.optimize(this.opts.code.optimize);
      let p = s.toString();
      if (d = `${s.scopeRefs(Uo.default.scope)}return ${p}`, this.opts.code.process) d = this.opts.code.process(d, e);
      let m = Function(`${Uo.default.self}`, `${Uo.default.scope}`, d)(this, this.scope.get());
      if (this.scope.value(c, { ref: m }), m.errors = null, m.schema = e.schema, m.schemaEnv = e, e.$async) m.$async = true;
      if (this.opts.code.source === true) m.source = { validateName: c, validateCode: p, scopeValues: s._values };
      if (this.opts.unevaluated) {
        let { props: g, items: h } = u;
        if (m.evaluated = { props: g instanceof gr.Name ? void 0 : g, items: h instanceof gr.Name ? void 0 : h, dynamicProps: g instanceof gr.Name, dynamicItems: h instanceof gr.Name }, m.source) m.source.evaluated = (0, gr.stringify)(m.evaluated);
      }
      return e.validate = m, e;
    } catch (p) {
      if (delete e.validate, delete e.validateName, d) this.logger.error("Error compiling schema, function code:", d);
      throw p;
    } finally {
      this._compilations.delete(e);
    }
  }
  IC.compileSchema = WS;
  function D3(e, t, r) {
    var o;
    r = (0, hr.resolveUrl)(this.opts.uriResolver, t, r);
    let n = e.refs[r];
    if (n) return n;
    let i = U3.call(this, e, r);
    if (i === void 0) {
      let s = (o = e.localRefs) === null || o === void 0 ? void 0 : o[r], { schemaId: a } = this.opts;
      if (s) i = new zl({ schema: s, schemaId: a, root: e, baseId: t });
    }
    if (i === void 0) return;
    return e.refs[r] = N3.call(this, i);
  }
  IC.resolveRef = D3;
  function N3(e) {
    if ((0, hr.inlineRef)(e.schema, this.opts.inlineRefs)) return e.schema;
    return e.validate ? e : WS.call(this, e);
  }
  function PC(e) {
    for (let t of this._compilations) if (j3(t, e)) return t;
  }
  IC.getCompilingSchema = PC;
  function j3(e, t) {
    return e.schema === t.schema && e.root === t.root && e.baseId === t.baseId;
  }
  function U3(e, t) {
    let r;
    while (typeof (r = this.refs[t]) == "string") t = r;
    return r || this.schemas[t] || Dm.call(this, e, t);
  }
  function Dm(e, t) {
    let r = this.opts.uriResolver.parse(t), o = (0, hr._getFullPath)(this.opts.uriResolver, r), n = (0, hr.getFullPath)(this.opts.uriResolver, e.baseId, void 0);
    if (Object.keys(e.schema).length > 0 && o === n) return VS.call(this, r, e);
    let i = (0, hr.normalizeId)(o), s = this.refs[i] || this.schemas[i];
    if (typeof s == "string") {
      let a = Dm.call(this, e, s);
      if (typeof (a === null || a === void 0 ? void 0 : a.schema) !== "object") return;
      return VS.call(this, r, a);
    }
    if (typeof (s === null || s === void 0 ? void 0 : s.schema) !== "object") return;
    if (!s.validate) WS.call(this, s);
    if (i === (0, hr.normalizeId)(t)) {
      let { schema: a } = s, { schemaId: c } = this.opts, u = a[c];
      if (u) n = (0, hr.resolveUrl)(this.opts.uriResolver, n, u);
      return new zl({ schema: a, schemaId: c, root: e, baseId: n });
    }
    return VS.call(this, r, s);
  }
  IC.resolveSchema = Dm;
  var z3 = /* @__PURE__ */ new Set(["properties", "patternProperties", "enum", "dependencies", "definitions"]);
  function VS(e, { baseId: t, schema: r, root: o }) {
    var n;
    if (((n = e.fragment) === null || n === void 0 ? void 0 : n[0]) !== "/") return;
    for (let a of e.fragment.slice(1).split("/")) {
      if (typeof r === "boolean") return;
      let c = r[(0, TC.unescapeFragment)(a)];
      if (c === void 0) return;
      r = c;
      let u = typeof r === "object" && r[this.opts.schemaId];
      if (!z3.has(a) && u) t = (0, hr.resolveUrl)(this.opts.uriResolver, t, u);
    }
    let i;
    if (typeof r != "boolean" && r.$ref && !(0, TC.schemaHasRulesButRef)(r, this.RULES)) {
      let a = (0, hr.resolveUrl)(this.opts.uriResolver, t, r.$ref);
      i = Dm.call(this, o, a);
    }
    let { schemaId: s } = this.opts;
    if (i = i || new zl({ schema: r, schemaId: s, root: o, baseId: t }), i.schema !== i.root.schema) return i;
    return;
  }
});
var $C = k((jEe, q3) => {
  q3.exports = { $id: "https://raw.githubusercontent.com/ajv-validator/ajv/master/lib/refs/data.json#", description: "Meta-schema for $data reference (JSON AnySchema extension proposal)", type: "object", required: ["$data"], properties: { $data: { type: "string", anyOf: [{ format: "relative-json-pointer" }, { format: "json-pointer" }] } }, additionalProperties: false };
});
var JS = k((UEe, NC) => {
  var Z3 = RegExp.prototype.test.bind(/^[\da-f]{8}-[\da-f]{4}-[\da-f]{4}-[\da-f]{4}-[\da-f]{12}$/iu), AC = RegExp.prototype.test.bind(/^(?:(?:25[0-5]|2[0-4]\d|1\d{2}|[1-9]\d|\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d{2}|[1-9]\d|\d)$/u), KS = RegExp.prototype.test.bind(/^[\da-f]{2}$/iu), CC = RegExp.prototype.test.bind(/^[\da-z\-._~]$/iu), V3 = RegExp.prototype.test.bind(/^[\da-z\-._~!$&'()*+,;=:@/]$/iu);
  function GS(e) {
    let t = "", r = 0, o = 0;
    for (o = 0; o < e.length; o++) {
      if (r = e[o].charCodeAt(0), r === 48) continue;
      if (!(r >= 48 && r <= 57 || r >= 65 && r <= 70 || r >= 97 && r <= 102)) return "";
      t += e[o];
      break;
    }
    for (o += 1; o < e.length; o++) {
      if (r = e[o].charCodeAt(0), !(r >= 48 && r <= 57 || r >= 65 && r <= 70 || r >= 97 && r <= 102)) return "";
      t += e[o];
    }
    return t;
  }
  var W3 = RegExp.prototype.test.bind(/[^!"$&'()*+,\-.;=_`a-z{}~]/u);
  function OC(e) {
    return e.length = 0, true;
  }
  function K3(e, t, r) {
    if (e.length) {
      let o = GS(e);
      if (o !== "") t.push(o);
      else return r.error = true, false;
      e.length = 0;
    }
    return true;
  }
  function G3(e) {
    let t = 0, r = { error: false, address: "", zone: "" }, o = [], n = [], i = false, s = false, a = K3;
    for (let c = 0; c < e.length; c++) {
      let u = e[c];
      if (u === "[" || u === "]") continue;
      if (u === ":") {
        if (i === true) s = true;
        if (!a(n, o, r)) break;
        if (++t > 7) {
          r.error = true;
          break;
        }
        if (c > 0 && e[c - 1] === ":") i = true;
        o.push(":");
        continue;
      } else if (u === "%") {
        if (!a(n, o, r)) break;
        a = OC;
      } else {
        n.push(u);
        continue;
      }
    }
    if (n.length) if (a === OC) r.zone = n.join("");
    else if (s) o.push(n.join(""));
    else o.push(GS(n));
    return r.address = o.join(""), r;
  }
  function MC(e) {
    if (J3(e, ":") < 2) return { host: e, isIPV6: false };
    let t = G3(e);
    if (!t.error) {
      let { address: r, address: o } = t;
      if (t.zone) r += "%" + t.zone, o += "%25" + t.zone;
      return { host: r, isIPV6: true, escapedHost: o };
    } else return { host: e, isIPV6: false };
  }
  function J3(e, t) {
    let r = 0;
    for (let o = 0; o < e.length; o++) if (e[o] === t) r++;
    return r;
  }
  function X3(e) {
    let t = e, r = [], o = -1, n = 0;
    while (n = t.length) {
      if (n === 1) if (t === ".") break;
      else if (t === "/") {
        r.push("/");
        break;
      } else {
        r.push(t);
        break;
      }
      else if (n === 2) {
        if (t[0] === ".") {
          if (t[1] === ".") break;
          else if (t[1] === "/") {
            t = t.slice(2);
            continue;
          }
        } else if (t[0] === "/") {
          if (t[1] === "." || t[1] === "/") {
            r.push("/");
            break;
          }
        }
      } else if (n === 3) {
        if (t === "/..") {
          if (r.length !== 0) r.pop();
          r.push("/");
          break;
        }
      }
      if (t[0] === ".") {
        if (t[1] === ".") {
          if (t[2] === "/") {
            t = t.slice(3);
            continue;
          }
        } else if (t[1] === "/") {
          t = t.slice(2);
          continue;
        }
      } else if (t[0] === "/") {
        if (t[1] === ".") {
          if (t[2] === "/") {
            t = t.slice(2);
            continue;
          } else if (t[2] === ".") {
            if (t[3] === "/") {
              if (t = t.slice(3), r.length !== 0) r.pop();
              continue;
            }
          }
        }
      }
      if ((o = t.indexOf("/", 1)) === -1) {
        r.push(t);
        break;
      } else r.push(t.slice(0, o)), t = t.slice(o);
    }
    return r.join("");
  }
  var Y3 = { "@": "%40", "/": "%2F", "?": "%3F", "#": "%23", ":": "%3A" }, Q3 = /[@/?#:]/g, e8 = /[@/?#]/g;
  function DC(e, t) {
    let r = t ? e8 : Q3;
    return r.lastIndex = 0, e.replace(r, (o) => Y3[o]);
  }
  function t8(e, t = false) {
    if (e.indexOf("%") === -1) return e;
    let r = "";
    for (let o = 0; o < e.length; o++) {
      if (e[o] === "%" && o + 2 < e.length) {
        let n = e.slice(o + 1, o + 3);
        if (KS(n)) {
          let i = n.toUpperCase(), s = String.fromCharCode(parseInt(i, 16));
          if (t && CC(s)) r += s;
          else r += "%" + i;
          o += 2;
          continue;
        }
      }
      r += e[o];
    }
    return r;
  }
  function r8(e) {
    let t = "";
    for (let r = 0; r < e.length; r++) {
      if (e[r] === "%" && r + 2 < e.length) {
        let o = e.slice(r + 1, r + 3);
        if (KS(o)) {
          let n = o.toUpperCase(), i = String.fromCharCode(parseInt(n, 16));
          if (i !== "." && CC(i)) t += i;
          else t += "%" + n;
          r += 2;
          continue;
        }
      }
      if (V3(e[r])) t += e[r];
      else t += escape(e[r]);
    }
    return t;
  }
  function n8(e) {
    let t = "";
    for (let r = 0; r < e.length; r++) {
      if (e[r] === "%" && r + 2 < e.length) {
        let o = e.slice(r + 1, r + 3);
        if (KS(o)) {
          t += "%" + o.toUpperCase(), r += 2;
          continue;
        }
      }
      t += escape(e[r]);
    }
    return t;
  }
  function o8(e) {
    let t = [];
    if (e.userinfo !== void 0) t.push(e.userinfo), t.push("@");
    if (e.host !== void 0) {
      let r = unescape(e.host);
      if (!AC(r)) {
        let o = MC(r);
        if (o.isIPV6 === true) r = `[${o.escapedHost}]`;
        else r = DC(r, false);
      }
      t.push(r);
    }
    if (typeof e.port === "number" || typeof e.port === "string") t.push(":"), t.push(String(e.port));
    return t.length ? t.join("") : void 0;
  }
  NC.exports = { nonSimpleDomain: W3, recomposeAuthority: o8, reescapeHostDelimiters: DC, normalizePercentEncoding: t8, normalizePathEncoding: r8, escapePreservingEscapes: n8, removeDotSegments: X3, isIPv4: AC, isUUID: Z3, normalizeIPv6: MC, stringArrayToHexStripped: GS };
});
var FC = k((zEe, LC) => {
  var { isUUID: i8 } = JS(), s8 = /([\da-z][\d\-a-z]{0,31}):((?:[\w!$'()*+,\-.:;=@]|%[\da-f]{2})+)/iu, a8 = ["http", "https", "ws", "wss", "urn", "urn:uuid"];
  function c8(e) {
    return a8.indexOf(e) !== -1;
  }
  function XS(e) {
    if (e.secure === true) return true;
    else if (e.secure === false) return false;
    else if (e.scheme) return e.scheme.length === 3 && (e.scheme[0] === "w" || e.scheme[0] === "W") && (e.scheme[1] === "s" || e.scheme[1] === "S") && (e.scheme[2] === "s" || e.scheme[2] === "S");
    else return false;
  }
  function jC(e) {
    if (!e.host) e.error = e.error || "HTTP URIs must have a host.";
    return e;
  }
  function UC(e) {
    let t = String(e.scheme).toLowerCase() === "https";
    if (e.port === (t ? 443 : 80) || e.port === "") e.port = void 0;
    if (!e.path) e.path = "/";
    return e;
  }
  function l8(e) {
    return e.secure = XS(e), e.resourceName = (e.path || "/") + (e.query ? "?" + e.query : ""), e.path = void 0, e.query = void 0, e;
  }
  function u8(e) {
    if (e.port === (XS(e) ? 443 : 80) || e.port === "") e.port = void 0;
    if (typeof e.secure === "boolean") e.scheme = e.secure ? "wss" : "ws", e.secure = void 0;
    if (e.resourceName) {
      let [t, r] = e.resourceName.split("?");
      e.path = t && t !== "/" ? t : void 0, e.query = r, e.resourceName = void 0;
    }
    return e.fragment = void 0, e;
  }
  function d8(e, t) {
    if (!e.path) return e.error = "URN can not be parsed", e;
    let r = e.path.match(s8);
    if (r) {
      let o = t.scheme || e.scheme || "urn";
      e.nid = r[1].toLowerCase(), e.nss = r[2];
      let n = `${o}:${t.nid || e.nid}`, i = YS(n);
      if (e.path = void 0, i) e = i.parse(e, t);
    } else e.error = e.error || "URN can not be parsed.";
    return e;
  }
  function p8(e, t) {
    if (e.nid === void 0) throw Error("URN without nid cannot be serialized");
    let r = t.scheme || e.scheme || "urn", o = e.nid.toLowerCase(), n = `${r}:${t.nid || o}`, i = YS(n);
    if (i) e = i.serialize(e, t);
    let s = e, a = e.nss;
    return s.path = `${o || t.nid}:${a}`, t.skipEscape = true, s;
  }
  function f8(e, t) {
    let r = e;
    if (r.uuid = r.nss, r.nss = void 0, !t.tolerant && (!r.uuid || !i8(r.uuid))) r.error = r.error || "UUID is not valid.";
    return r;
  }
  function m8(e) {
    let t = e;
    return t.nss = (e.uuid || "").toLowerCase(), t;
  }
  var zC = { scheme: "http", domainHost: true, parse: jC, serialize: UC }, g8 = { scheme: "https", domainHost: zC.domainHost, parse: jC, serialize: UC }, jm = { scheme: "ws", domainHost: true, parse: l8, serialize: u8 }, h8 = { scheme: "wss", domainHost: jm.domainHost, parse: jm.parse, serialize: jm.serialize }, y8 = { scheme: "urn", parse: d8, serialize: p8, skipNormalize: true }, b8 = { scheme: "urn:uuid", parse: f8, serialize: m8, skipNormalize: true }, Um = { http: zC, https: g8, ws: jm, wss: h8, urn: y8, "urn:uuid": b8 };
  Object.setPrototypeOf(Um, null);
  function YS(e) {
    return e && (Um[e] || Um[e.toLowerCase()]) || void 0;
  }
  LC.exports = { wsIsSecure: XS, SCHEMES: Um, isValidSchemeName: c8, getSchemeHandler: YS };
});
var WC = k((LEe, zm) => {
  var { normalizeIPv6: _8, removeDotSegments: Ll, recomposeAuthority: v8, normalizePercentEncoding: S8, normalizePathEncoding: x8, escapePreservingEscapes: w8, reescapeHostDelimiters: k8, isIPv4: E8, nonSimpleDomain: T8 } = JS(), { SCHEMES: P8, getSchemeHandler: HC } = FC();
  function I8(e, t) {
    if (typeof e === "string") e = C8(e, t);
    else if (typeof e === "object") e = Ss(zo(e, t), t);
    return e;
  }
  function R8(e, t, r) {
    let o = r ? Object.assign({ scheme: "null" }, r) : { scheme: "null" }, n = qC(Ss(e, o), Ss(t, o), o, true);
    return o.skipEscape = true, zo(n, o);
  }
  function qC(e, t, r, o) {
    let n = {};
    if (!o) e = Ss(zo(e, r), r), t = Ss(zo(t, r), r);
    if (r = r || {}, !r.tolerant && t.scheme) n.scheme = t.scheme, n.userinfo = t.userinfo, n.host = t.host, n.port = t.port, n.path = Ll(t.path || ""), n.query = t.query;
    else {
      if (t.userinfo !== void 0 || t.host !== void 0 || t.port !== void 0) n.userinfo = t.userinfo, n.host = t.host, n.port = t.port, n.path = Ll(t.path || ""), n.query = t.query;
      else {
        if (!t.path) if (n.path = e.path, t.query !== void 0) n.query = t.query;
        else n.query = e.query;
        else {
          if (t.path[0] === "/") n.path = Ll(t.path);
          else {
            if ((e.userinfo !== void 0 || e.host !== void 0 || e.port !== void 0) && !e.path) n.path = "/" + t.path;
            else if (!e.path) n.path = t.path;
            else n.path = e.path.slice(0, e.path.lastIndexOf("/") + 1) + t.path;
            n.path = Ll(n.path);
          }
          n.query = t.query;
        }
        n.userinfo = e.userinfo, n.host = e.host, n.port = e.port;
      }
      n.scheme = e.scheme;
    }
    return n.fragment = t.fragment, n;
  }
  function $8(e, t, r) {
    let o = BC(e, r), n = BC(t, r);
    return o !== void 0 && n !== void 0 && o.toLowerCase() === n.toLowerCase();
  }
  function zo(e, t) {
    let r = { host: e.host, scheme: e.scheme, userinfo: e.userinfo, port: e.port, path: e.path, query: e.query, nid: e.nid, nss: e.nss, uuid: e.uuid, fragment: e.fragment, reference: e.reference, resourceName: e.resourceName, secure: e.secure, error: "" }, o = Object.assign({}, t), n = [], i = HC(o.scheme || r.scheme);
    if (i && i.serialize) i.serialize(r, o);
    if (r.path !== void 0) if (!o.skipEscape) {
      if (r.path = w8(r.path), r.scheme !== void 0) r.path = r.path.split("%3A").join(":");
    } else r.path = S8(r.path);
    if (o.reference !== "suffix" && r.scheme) n.push(r.scheme, ":");
    let s = v8(r);
    if (s !== void 0) {
      if (o.reference !== "suffix") n.push("//");
      if (n.push(s), r.path && r.path[0] !== "/") n.push("/");
    }
    if (r.path !== void 0) {
      let a = r.path;
      if (!o.absolutePath && (!i || !i.absolutePath)) a = Ll(a);
      if (s === void 0 && a[0] === "/" && a[1] === "/") a = "/%2F" + a.slice(2);
      n.push(a);
    }
    if (r.query !== void 0) n.push("?", r.query);
    if (r.fragment !== void 0) n.push("#", r.fragment);
    return n.join("");
  }
  var O8 = /^(?:([^#/:?]+):)?(?:\/\/((?:([^#/?@]*)@)?(\[[^#/?\]]+\]|[^#/:?]*)(?::(\d*))?))?([^#?]*)(?:\?([^#]*))?(?:#((?:.|[\n\r])*))?/u;
  function A8(e, t) {
    if (t[2] !== void 0 && e.path && e.path[0] !== "/") return 'URI path must start with "/" when authority is present.';
    if (typeof e.port === "number" && (e.port < 0 || e.port > 65535)) return "URI port is malformed.";
    return;
  }
  function ZC(e, t) {
    let r = Object.assign({}, t), o = { scheme: void 0, userinfo: void 0, host: "", port: void 0, path: "", query: void 0, fragment: void 0 }, n = false, i = false;
    if (r.reference === "suffix") if (r.scheme) e = r.scheme + ":" + e;
    else e = "//" + e;
    let s = e.match(O8);
    if (s) {
      if (o.scheme = s[1], o.userinfo = s[3], o.host = s[4], o.port = parseInt(s[5], 10), o.path = s[6] || "", o.query = s[7], o.fragment = s[8], isNaN(o.port)) o.port = s[5];
      let a = A8(o, s);
      if (a !== void 0) o.error = o.error || a, n = true;
      if (o.host) if (E8(o.host) === false) {
        let d = _8(o.host);
        o.host = d.host.toLowerCase(), i = d.isIPV6;
      } else i = true;
      if (o.scheme === void 0 && o.userinfo === void 0 && o.host === void 0 && o.port === void 0 && o.query === void 0 && !o.path) o.reference = "same-document";
      else if (o.scheme === void 0) o.reference = "relative";
      else if (o.fragment === void 0) o.reference = "absolute";
      else o.reference = "uri";
      if (r.reference && r.reference !== "suffix" && r.reference !== o.reference) o.error = o.error || "URI is not a " + r.reference + " reference.";
      let c = HC(r.scheme || o.scheme);
      if (!r.unicodeSupport && (!c || !c.unicodeSupport)) {
        if (o.host && (r.domainHost || c && c.domainHost) && i === false && T8(o.host)) try {
          o.host = URL.domainToASCII(o.host.toLowerCase());
        } catch (u) {
          o.error = o.error || "Host's domain name can not be converted to ASCII: " + u;
        }
      }
      if (!c || c && !c.skipNormalize) {
        if (e.indexOf("%") !== -1) {
          if (o.scheme !== void 0) o.scheme = unescape(o.scheme);
          if (o.host !== void 0) o.host = k8(unescape(o.host), i);
        }
        if (o.path) o.path = x8(o.path);
        if (o.fragment) try {
          o.fragment = encodeURI(decodeURIComponent(o.fragment));
        } catch {
          o.error = o.error || "URI malformed";
        }
      }
      if (c && c.parse) c.parse(o, r);
    } else o.error = o.error || "URI can not be parsed.";
    return { parsed: o, malformedAuthorityOrPort: n };
  }
  function Ss(e, t) {
    return ZC(e, t).parsed;
  }
  function C8(e, t) {
    return VC(e, t).normalized;
  }
  function VC(e, t) {
    let { parsed: r, malformedAuthorityOrPort: o } = ZC(e, t);
    return { normalized: o ? e : zo(r, t), malformedAuthorityOrPort: o };
  }
  function BC(e, t) {
    if (typeof e === "string") {
      let { normalized: r, malformedAuthorityOrPort: o } = VC(e, t);
      return o ? void 0 : r;
    }
    if (typeof e === "object") return zo(e, t);
  }
  var QS = { SCHEMES: P8, normalize: I8, resolve: R8, resolveComponent: qC, equal: $8, serialize: zo, parse: Ss };
  zm.exports = QS;
  zm.exports.default = QS;
  zm.exports.fastUri = QS;
});
var JC = k((GC) => {
  Object.defineProperty(GC, "__esModule", { value: true });
  var KC = WC();
  KC.code = 'require("ajv/dist/runtime/uri").default';
  GC.default = KC;
});
var oM = k((nn) => {
  Object.defineProperty(nn, "__esModule", { value: true });
  nn.CodeGen = nn.Name = nn.nil = nn.stringify = nn.str = nn._ = nn.KeywordCxt = void 0;
  var D8 = jl();
  Object.defineProperty(nn, "KeywordCxt", { enumerable: true, get: function() {
    return D8.KeywordCxt;
  } });
  var xs = re();
  Object.defineProperty(nn, "_", { enumerable: true, get: function() {
    return xs._;
  } });
  Object.defineProperty(nn, "str", { enumerable: true, get: function() {
    return xs.str;
  } });
  Object.defineProperty(nn, "stringify", { enumerable: true, get: function() {
    return xs.stringify;
  } });
  Object.defineProperty(nn, "nil", { enumerable: true, get: function() {
    return xs.nil;
  } });
  Object.defineProperty(nn, "Name", { enumerable: true, get: function() {
    return xs.Name;
  } });
  Object.defineProperty(nn, "CodeGen", { enumerable: true, get: function() {
    return xs.CodeGen;
  } });
  var N8 = Mm(), tM = Ul(), j8 = OS(), Fl = Nm(), U8 = re(), Bl = Ml(), Lm = Cl(), tx = ue(), XC = $C(), z8 = JC(), rM = (e, t) => new RegExp(e, t);
  rM.code = "new RegExp";
  var L8 = ["removeAdditional", "useDefaults", "coerceTypes"], F8 = /* @__PURE__ */ new Set(["validate", "serialize", "parse", "wrapper", "root", "schema", "keyword", "pattern", "formats", "validate$data", "func", "obj", "Error"]), B8 = { errorDataPath: "", format: "`validateFormats: false` can be used instead.", nullable: '"nullable" keyword is supported by default.', jsonPointers: "Deprecated jsPropertySyntax can be used instead.", extendRefs: "Deprecated ignoreKeywordsWithRef can be used instead.", missingRefs: "Pass empty schema with $id that should be ignored to ajv.addSchema.", processCode: "Use option `code: {process: (code, schemaEnv: object) => string}`", sourceCode: "Use option `code: {source: true}`", strictDefaults: "It is default now, see option `strict`.", strictKeywords: "It is default now, see option `strict`.", uniqueItems: '"uniqueItems" keyword is always validated.', unknownFormats: "Disable strict mode or pass `true` to `ajv.addFormat` (or `formats` option).", cache: "Map is used as cache, schema object as key.", serialize: "Map is used as cache, schema object as key.", ajvErrors: "It is default now." }, H8 = { ignoreKeywordsWithRef: "", jsPropertySyntax: "", unicode: '"minLength"/"maxLength" account for unicode characters by default.' }, YC = 200;
  function q8(e) {
    var t, r, o, n, i, s, a, c, u, d, p, f, m, g, h, y, v, x, w, A, U, se, Le, Ye, Lt;
    let yt = e.strict, Qn = (t = e.code) === null || t === void 0 ? void 0 : t.optimize, Go = Qn === true || Qn === void 0 ? 1 : Qn || 0, Rr = (o = (r = e.code) === null || r === void 0 ? void 0 : r.regExp) !== null && o !== void 0 ? o : rM, js = (n = e.uriResolver) !== null && n !== void 0 ? n : z8.default;
    return { strictSchema: (s = (i = e.strictSchema) !== null && i !== void 0 ? i : yt) !== null && s !== void 0 ? s : true, strictNumbers: (c = (a = e.strictNumbers) !== null && a !== void 0 ? a : yt) !== null && c !== void 0 ? c : true, strictTypes: (d = (u = e.strictTypes) !== null && u !== void 0 ? u : yt) !== null && d !== void 0 ? d : "log", strictTuples: (f = (p = e.strictTuples) !== null && p !== void 0 ? p : yt) !== null && f !== void 0 ? f : "log", strictRequired: (g = (m = e.strictRequired) !== null && m !== void 0 ? m : yt) !== null && g !== void 0 ? g : false, code: e.code ? { ...e.code, optimize: Go, regExp: Rr } : { optimize: Go, regExp: Rr }, loopRequired: (h = e.loopRequired) !== null && h !== void 0 ? h : YC, loopEnum: (y = e.loopEnum) !== null && y !== void 0 ? y : YC, meta: (v = e.meta) !== null && v !== void 0 ? v : true, messages: (x = e.messages) !== null && x !== void 0 ? x : true, inlineRefs: (w = e.inlineRefs) !== null && w !== void 0 ? w : true, schemaId: (A = e.schemaId) !== null && A !== void 0 ? A : "$id", addUsedSchema: (U = e.addUsedSchema) !== null && U !== void 0 ? U : true, validateSchema: (se = e.validateSchema) !== null && se !== void 0 ? se : true, validateFormats: (Le = e.validateFormats) !== null && Le !== void 0 ? Le : true, unicodeRegExp: (Ye = e.unicodeRegExp) !== null && Ye !== void 0 ? Ye : true, int32range: (Lt = e.int32range) !== null && Lt !== void 0 ? Lt : true, uriResolver: js };
  }
  class Fm {
    constructor(e = {}) {
      this.schemas = {}, this.refs = {}, this.formats = {}, this._compilations = /* @__PURE__ */ new Set(), this._loading = {}, this._cache = /* @__PURE__ */ new Map(), e = this.opts = { ...e, ...q8(e) };
      let { es5: t, lines: r } = this.opts.code;
      this.scope = new U8.ValueScope({ scope: {}, prefixes: F8, es5: t, lines: r }), this.logger = J8(e.logger);
      let o = e.validateFormats;
      if (e.validateFormats = false, this.RULES = (0, j8.getRules)(), QC.call(this, B8, e, "NOT SUPPORTED"), QC.call(this, H8, e, "DEPRECATED", "warn"), this._metaOpts = K8.call(this), e.formats) V8.call(this);
      if (this._addVocabularies(), this._addDefaultMetaSchema(), e.keywords) W8.call(this, e.keywords);
      if (typeof e.meta == "object") this.addMetaSchema(e.meta);
      Z8.call(this), e.validateFormats = o;
    }
    _addVocabularies() {
      this.addKeyword("$async");
    }
    _addDefaultMetaSchema() {
      let { $data: e, meta: t, schemaId: r } = this.opts, o = XC;
      if (r === "id") o = { ...XC }, o.id = o.$id, delete o.$id;
      if (t && e) this.addMetaSchema(o, o[r], false);
    }
    defaultMeta() {
      let { meta: e, schemaId: t } = this.opts;
      return this.opts.defaultMeta = typeof e == "object" ? e[t] || e : void 0;
    }
    validate(e, t) {
      let r;
      if (typeof e == "string") {
        if (r = this.getSchema(e), !r) throw Error(`no schema with key or ref "${e}"`);
      } else r = this.compile(e);
      let o = r(t);
      if (!("$async" in r)) this.errors = r.errors;
      return o;
    }
    compile(e, t) {
      let r = this._addSchema(e, t);
      return r.validate || this._compileSchemaEnv(r);
    }
    compileAsync(e, t) {
      if (typeof this.opts.loadSchema != "function") throw Error("options.loadSchema should be a function");
      let { loadSchema: r } = this.opts;
      return o.call(this, e, t);
      async function o(u, d) {
        await n.call(this, u.$schema);
        let p = this._addSchema(u, d);
        return p.validate || i.call(this, p);
      }
      async function n(u) {
        if (u && !this.getSchema(u)) await o.call(this, { $ref: u }, true);
      }
      async function i(u) {
        try {
          return this._compileSchemaEnv(u);
        } catch (d) {
          if (!(d instanceof tM.default)) throw d;
          return s.call(this, d), await a.call(this, d.missingSchema), i.call(this, u);
        }
      }
      function s({ missingSchema: u, missingRef: d }) {
        if (this.refs[u]) throw Error(`AnySchema ${u} is loaded but ${d} cannot be resolved`);
      }
      async function a(u) {
        let d = await c.call(this, u);
        if (!this.refs[u]) await n.call(this, d.$schema);
        if (!this.refs[u]) this.addSchema(d, u, t);
      }
      async function c(u) {
        let d = this._loading[u];
        if (d) return d;
        try {
          return await (this._loading[u] = r(u));
        } finally {
          delete this._loading[u];
        }
      }
    }
    addSchema(e, t, r, o = this.opts.validateSchema) {
      if (Array.isArray(e)) {
        for (let i of e) this.addSchema(i, void 0, r, o);
        return this;
      }
      let n;
      if (typeof e === "object") {
        let { schemaId: i } = this.opts;
        if (n = e[i], n !== void 0 && typeof n != "string") throw Error(`schema ${i} must be string`);
      }
      return t = (0, Bl.normalizeId)(t || n), this._checkUnique(t), this.schemas[t] = this._addSchema(e, r, t, o, true), this;
    }
    addMetaSchema(e, t, r = this.opts.validateSchema) {
      return this.addSchema(e, t, true, r), this;
    }
    validateSchema(e, t) {
      if (typeof e == "boolean") return true;
      let r;
      if (r = e.$schema, r !== void 0 && typeof r != "string") throw Error("$schema must be a string");
      if (r = r || this.opts.defaultMeta || this.defaultMeta(), !r) return this.logger.warn("meta-schema not available"), this.errors = null, true;
      let o = this.validate(r, e);
      if (!o && t) {
        let n = "schema is invalid: " + this.errorsText();
        if (this.opts.validateSchema === "log") this.logger.error(n);
        else throw Error(n);
      }
      return o;
    }
    getSchema(e) {
      let t;
      while (typeof (t = eM.call(this, e)) == "string") e = t;
      if (t === void 0) {
        let { schemaId: r } = this.opts, o = new Fl.SchemaEnv({ schema: {}, schemaId: r });
        if (t = Fl.resolveSchema.call(this, o, e), !t) return;
        this.refs[e] = t;
      }
      return t.validate || this._compileSchemaEnv(t);
    }
    removeSchema(e) {
      if (e instanceof RegExp) return this._removeAllSchemas(this.schemas, e), this._removeAllSchemas(this.refs, e), this;
      switch (typeof e) {
        case "undefined":
          return this._removeAllSchemas(this.schemas), this._removeAllSchemas(this.refs), this._cache.clear(), this;
        case "string": {
          let t = eM.call(this, e);
          if (typeof t == "object") this._cache.delete(t.schema);
          return delete this.schemas[e], delete this.refs[e], this;
        }
        case "object": {
          let t = e;
          this._cache.delete(t);
          let r = e[this.opts.schemaId];
          if (r) r = (0, Bl.normalizeId)(r), delete this.schemas[r], delete this.refs[r];
          return this;
        }
        default:
          throw Error("ajv.removeSchema: invalid parameter");
      }
    }
    addVocabulary(e) {
      for (let t of e) this.addKeyword(t);
      return this;
    }
    addKeyword(e, t) {
      let r;
      if (typeof e == "string") {
        if (r = e, typeof t == "object") this.logger.warn("these parameters are deprecated, see docs for addKeyword"), t.keyword = r;
      } else if (typeof e == "object" && t === void 0) {
        if (t = e, r = t.keyword, Array.isArray(r) && !r.length) throw Error("addKeywords: keyword must be string or non-empty array");
      } else throw Error("invalid addKeywords parameters");
      if (Y8.call(this, r, t), !t) return (0, tx.eachItem)(r, (n) => ex.call(this, n)), this;
      eX.call(this, t);
      let o = { ...t, type: (0, Lm.getJSONTypes)(t.type), schemaType: (0, Lm.getJSONTypes)(t.schemaType) };
      return (0, tx.eachItem)(r, o.type.length === 0 ? (n) => ex.call(this, n, o) : (n) => o.type.forEach((i) => ex.call(this, n, o, i))), this;
    }
    getKeyword(e) {
      let t = this.RULES.all[e];
      return typeof t == "object" ? t.definition : !!t;
    }
    removeKeyword(e) {
      let { RULES: t } = this;
      delete t.keywords[e], delete t.all[e];
      for (let r of t.rules) {
        let o = r.rules.findIndex((n) => n.keyword === e);
        if (o >= 0) r.rules.splice(o, 1);
      }
      return this;
    }
    addFormat(e, t) {
      if (typeof t == "string") t = new RegExp(t);
      return this.formats[e] = t, this;
    }
    errorsText(e = this.errors, { separator: t = ", ", dataVar: r = "data" } = {}) {
      if (!e || e.length === 0) return "No errors";
      return e.map((o) => `${r}${o.instancePath} ${o.message}`).reduce((o, n) => o + t + n);
    }
    $dataMetaSchema(e, t) {
      let r = this.RULES.all;
      e = JSON.parse(JSON.stringify(e));
      for (let o of t) {
        let n = o.split("/").slice(1), i = e;
        for (let s of n) i = i[s];
        for (let s in r) {
          let a = r[s];
          if (typeof a != "object") continue;
          let { $data: c } = a.definition, u = i[s];
          if (c && u) i[s] = nM(u);
        }
      }
      return e;
    }
    _removeAllSchemas(e, t) {
      for (let r in e) {
        let o = e[r];
        if (!t || t.test(r)) {
          if (typeof o == "string") delete e[r];
          else if (o && !o.meta) this._cache.delete(o.schema), delete e[r];
        }
      }
    }
    _addSchema(e, t, r, o = this.opts.validateSchema, n = this.opts.addUsedSchema) {
      let i, { schemaId: s } = this.opts;
      if (typeof e == "object") i = e[s];
      else if (this.opts.jtd) throw Error("schema must be object");
      else if (typeof e != "boolean") throw Error("schema must be object or boolean");
      let a = this._cache.get(e);
      if (a !== void 0) return a;
      r = (0, Bl.normalizeId)(i || r);
      let c = Bl.getSchemaRefs.call(this, e, r);
      if (a = new Fl.SchemaEnv({ schema: e, schemaId: s, meta: t, baseId: r, localRefs: c }), this._cache.set(a.schema, a), n && !r.startsWith("#")) {
        if (r) this._checkUnique(r);
        this.refs[r] = a;
      }
      if (o) this.validateSchema(e, true);
      return a;
    }
    _checkUnique(e) {
      if (this.schemas[e] || this.refs[e]) throw Error(`schema with key or id "${e}" already exists`);
    }
    _compileSchemaEnv(e) {
      if (e.meta) this._compileMetaSchema(e);
      else Fl.compileSchema.call(this, e);
      if (!e.validate) throw Error("ajv implementation error");
      return e.validate;
    }
    _compileMetaSchema(e) {
      let t = this.opts;
      this.opts = this._metaOpts;
      try {
        Fl.compileSchema.call(this, e);
      } finally {
        this.opts = t;
      }
    }
  }
  Fm.ValidationError = N8.default;
  Fm.MissingRefError = tM.default;
  nn.default = Fm;
  function QC(e, t, r, o = "error") {
    for (let n in e) {
      let i = n;
      if (i in t) this.logger[o](`${r}: option ${n}. ${e[i]}`);
    }
  }
  function eM(e) {
    return e = (0, Bl.normalizeId)(e), this.schemas[e] || this.refs[e];
  }
  function Z8() {
    let e = this.opts.schemas;
    if (!e) return;
    if (Array.isArray(e)) this.addSchema(e);
    else for (let t in e) this.addSchema(e[t], t);
  }
  function V8() {
    for (let e in this.opts.formats) {
      let t = this.opts.formats[e];
      if (t) this.addFormat(e, t);
    }
  }
  function W8(e) {
    if (Array.isArray(e)) {
      this.addVocabulary(e);
      return;
    }
    this.logger.warn("keywords option as map is deprecated, pass array");
    for (let t in e) {
      let r = e[t];
      if (!r.keyword) r.keyword = t;
      this.addKeyword(r);
    }
  }
  function K8() {
    let e = { ...this.opts };
    for (let t of L8) delete e[t];
    return e;
  }
  var G8 = { log() {
  }, warn() {
  }, error() {
  } };
  function J8(e) {
    if (e === false) return G8;
    if (e === void 0) return console;
    if (e.log && e.warn && e.error) return e;
    throw Error("logger must implement log, warn and error methods");
  }
  var X8 = /^[a-z_$][a-z0-9_$:-]*$/i;
  function Y8(e, t) {
    let { RULES: r } = this;
    if ((0, tx.eachItem)(e, (o) => {
      if (r.keywords[o]) throw Error(`Keyword ${o} is already defined`);
      if (!X8.test(o)) throw Error(`Keyword ${o} has invalid name`);
    }), !t) return;
    if (t.$data && !("code" in t || "validate" in t)) throw Error('$data keyword must have "code" or "validate" function');
  }
  function ex(e, t, r) {
    var o;
    let n = t === null || t === void 0 ? void 0 : t.post;
    if (r && n) throw Error('keyword with "post" flag cannot have "type"');
    let { RULES: i } = this, s = n ? i.post : i.rules.find(({ type: c }) => c === r);
    if (!s) s = { type: r, rules: [] }, i.rules.push(s);
    if (i.keywords[e] = true, !t) return;
    let a = { keyword: e, definition: { ...t, type: (0, Lm.getJSONTypes)(t.type), schemaType: (0, Lm.getJSONTypes)(t.schemaType) } };
    if (t.before) Q8.call(this, s, a, t.before);
    else s.rules.push(a);
    i.all[e] = a, (o = t.implements) === null || o === void 0 || o.forEach((c) => this.addKeyword(c));
  }
  function Q8(e, t, r) {
    let o = e.rules.findIndex((n) => n.keyword === r);
    if (o >= 0) e.rules.splice(o, 0, t);
    else e.rules.push(t), this.logger.warn(`rule ${r} is not defined`);
  }
  function eX(e) {
    let { metaSchema: t } = e;
    if (t === void 0) return;
    if (e.$data && this.opts.$data) t = nM(t);
    e.validateSchema = this.compile(t, true);
  }
  var tX = { $ref: "https://raw.githubusercontent.com/ajv-validator/ajv/master/lib/refs/data.json#" };
  function nM(e) {
    return { anyOf: [e, tX] };
  }
});
var sM = k((iM) => {
  Object.defineProperty(iM, "__esModule", { value: true });
  var oX = { keyword: "id", code() {
    throw Error('NOT SUPPORTED: keyword "id", use "$id" for schema ID');
  } };
  iM.default = oX;
});
var pM = k((uM) => {
  Object.defineProperty(uM, "__esModule", { value: true });
  uM.callRef = uM.getValidate = void 0;
  var sX = Ul(), aM = er(), It = re(), ws = tn(), cM = Nm(), Bm = ue(), aX = { keyword: "$ref", schemaType: "string", code(e) {
    let { gen: t, schema: r, it: o } = e, { baseId: n, schemaEnv: i, validateName: s, opts: a, self: c } = o, { root: u } = i;
    if ((r === "#" || r === "#/") && n === u.baseId) return p();
    let d = cM.resolveRef.call(c, u, n, r);
    if (d === void 0) throw new sX.default(o.opts.uriResolver, n, r);
    if (d instanceof cM.SchemaEnv) return f(d);
    return m(d);
    function p() {
      if (i === u) return Hm(e, s, i, i.$async);
      let g = t.scopeValue("root", { ref: u });
      return Hm(e, It._`${g}.validate`, u, u.$async);
    }
    function f(g) {
      let h = lM(e, g);
      Hm(e, h, g, g.$async);
    }
    function m(g) {
      let h = t.scopeValue("schema", a.code.source === true ? { ref: g, code: (0, It.stringify)(g) } : { ref: g }), y = t.name("valid"), v = e.subschema({ schema: g, dataTypes: [], schemaPath: It.nil, topSchemaRef: h, errSchemaPath: r }, y);
      e.mergeEvaluated(v), e.ok(y);
    }
  } };
  function lM(e, t) {
    let { gen: r } = e;
    return t.validate ? r.scopeValue("validate", { ref: t.validate }) : It._`${r.scopeValue("wrapper", { ref: t })}.validate`;
  }
  uM.getValidate = lM;
  function Hm(e, t, r, o) {
    let { gen: n, it: i } = e, { allErrors: s, schemaEnv: a, opts: c } = i, u = c.passContext ? ws.default.this : It.nil;
    if (o) d();
    else p();
    function d() {
      if (!a.$async) throw Error("async schema referenced by sync schema");
      let g = n.let("valid");
      n.try(() => {
        if (n.code(It._`await ${(0, aM.callValidateCode)(e, t, u)}`), m(t), !s) n.assign(g, true);
      }, (h) => {
        if (n.if(It._`!(${h} instanceof ${i.ValidationError})`, () => n.throw(h)), f(h), !s) n.assign(g, false);
      }), e.ok(g);
    }
    function p() {
      e.result((0, aM.callValidateCode)(e, t, u), () => m(t), () => f(t));
    }
    function f(g) {
      let h = It._`${g}.errors`;
      n.assign(ws.default.vErrors, It._`${ws.default.vErrors} === null ? ${h} : ${ws.default.vErrors}.concat(${h})`), n.assign(ws.default.errors, It._`${ws.default.vErrors}.length`);
    }
    function m(g) {
      var h;
      if (!i.opts.unevaluated) return;
      let y = (h = r === null || r === void 0 ? void 0 : r.validate) === null || h === void 0 ? void 0 : h.evaluated;
      if (i.props !== true) if (y && !y.dynamicProps) {
        if (y.props !== void 0) i.props = Bm.mergeEvaluated.props(n, y.props, i.props);
      } else {
        let v = n.var("props", It._`${g}.evaluated.props`);
        i.props = Bm.mergeEvaluated.props(n, v, i.props, It.Name);
      }
      if (i.items !== true) if (y && !y.dynamicItems) {
        if (y.items !== void 0) i.items = Bm.mergeEvaluated.items(n, y.items, i.items);
      } else {
        let v = n.var("items", It._`${g}.evaluated.items`);
        i.items = Bm.mergeEvaluated.items(n, v, i.items, It.Name);
      }
    }
  }
  uM.callRef = Hm;
  uM.default = aX;
});
var mM = k((fM) => {
  Object.defineProperty(fM, "__esModule", { value: true });
  var uX = sM(), dX = pM(), pX = ["$schema", "$id", "$defs", "$vocabulary", { keyword: "$comment" }, "definitions", uX.default, dX.default];
  fM.default = pX;
});
var hM = k((gM) => {
  Object.defineProperty(gM, "__esModule", { value: true });
  var qm = re(), Hn = qm.operators, Zm = { maximum: { okStr: "<=", ok: Hn.LTE, fail: Hn.GT }, minimum: { okStr: ">=", ok: Hn.GTE, fail: Hn.LT }, exclusiveMaximum: { okStr: "<", ok: Hn.LT, fail: Hn.GTE }, exclusiveMinimum: { okStr: ">", ok: Hn.GT, fail: Hn.LTE } }, mX = { message: ({ keyword: e, schemaCode: t }) => qm.str`must be ${Zm[e].okStr} ${t}`, params: ({ keyword: e, schemaCode: t }) => qm._`{comparison: ${Zm[e].okStr}, limit: ${t}}` }, gX = { keyword: Object.keys(Zm), type: "number", schemaType: "number", $data: true, error: mX, code(e) {
    let { keyword: t, data: r, schemaCode: o } = e;
    e.fail$data(qm._`${r} ${Zm[t].fail} ${o} || isNaN(${r})`);
  } };
  gM.default = gX;
});
var bM = k((yM) => {
  Object.defineProperty(yM, "__esModule", { value: true });
  var Hl = re(), yX = { message: ({ schemaCode: e }) => Hl.str`must be multiple of ${e}`, params: ({ schemaCode: e }) => Hl._`{multipleOf: ${e}}` }, bX = { keyword: "multipleOf", type: "number", schemaType: "number", $data: true, error: yX, code(e) {
    let { gen: t, data: r, schemaCode: o, it: n } = e, i = n.opts.multipleOfPrecision, s = t.let("res"), a = i ? Hl._`Math.abs(Math.round(${s}) - ${s}) > 1e-${i}` : Hl._`${s} !== parseInt(${s})`;
    e.fail$data(Hl._`(${o} === 0 || (${s} = ${r}/${o}, ${a}))`);
  } };
  yM.default = bX;
});
var SM = k((vM) => {
  Object.defineProperty(vM, "__esModule", { value: true });
  function _M(e) {
    let t = e.length, r = 0, o = 0, n;
    while (o < t) if (r++, n = e.charCodeAt(o++), n >= 55296 && n <= 56319 && o < t) {
      if (n = e.charCodeAt(o), (n & 64512) === 56320) o++;
    }
    return r;
  }
  vM.default = _M;
  _M.code = 'require("ajv/dist/runtime/ucs2length").default';
});
var wM = k((xM) => {
  Object.defineProperty(xM, "__esModule", { value: true });
  var Lo = re(), SX = ue(), xX = SM(), wX = { message({ keyword: e, schemaCode: t }) {
    let r = e === "maxLength" ? "more" : "fewer";
    return Lo.str`must NOT have ${r} than ${t} characters`;
  }, params: ({ schemaCode: e }) => Lo._`{limit: ${e}}` }, kX = { keyword: ["maxLength", "minLength"], type: "string", schemaType: "number", $data: true, error: wX, code(e) {
    let { keyword: t, data: r, schemaCode: o, it: n } = e, i = t === "maxLength" ? Lo.operators.GT : Lo.operators.LT, s = n.opts.unicode === false ? Lo._`${r}.length` : Lo._`${(0, SX.useFunc)(e.gen, xX.default)}(${r})`;
    e.fail$data(Lo._`${s} ${i} ${o}`);
  } };
  xM.default = kX;
});
var EM = k((kM) => {
  Object.defineProperty(kM, "__esModule", { value: true });
  var TX = er(), PX = ue(), ks = re(), IX = { message: ({ schemaCode: e }) => ks.str`must match pattern "${e}"`, params: ({ schemaCode: e }) => ks._`{pattern: ${e}}` }, RX = { keyword: "pattern", type: "string", schemaType: "string", $data: true, error: IX, code(e) {
    let { gen: t, data: r, $data: o, schema: n, schemaCode: i, it: s } = e, a = s.opts.unicodeRegExp ? "u" : "";
    if (o) {
      let { regExp: c } = s.opts.code, u = c.code === "new RegExp" ? ks._`new RegExp` : (0, PX.useFunc)(t, c), d = t.let("valid");
      t.try(() => t.assign(d, ks._`${u}(${i}, ${a}).test(${r})`), () => t.assign(d, false)), e.fail$data(ks._`!${d}`);
    } else {
      let c = (0, TX.usePattern)(e, n);
      e.fail$data(ks._`!${c}.test(${r})`);
    }
  } };
  kM.default = RX;
});
var PM = k((TM) => {
  Object.defineProperty(TM, "__esModule", { value: true });
  var ql = re(), OX = { message({ keyword: e, schemaCode: t }) {
    let r = e === "maxProperties" ? "more" : "fewer";
    return ql.str`must NOT have ${r} than ${t} properties`;
  }, params: ({ schemaCode: e }) => ql._`{limit: ${e}}` }, AX = { keyword: ["maxProperties", "minProperties"], type: "object", schemaType: "number", $data: true, error: OX, code(e) {
    let { keyword: t, data: r, schemaCode: o } = e, n = t === "maxProperties" ? ql.operators.GT : ql.operators.LT;
    e.fail$data(ql._`Object.keys(${r}).length ${n} ${o}`);
  } };
  TM.default = AX;
});
var RM = k((IM) => {
  Object.defineProperty(IM, "__esModule", { value: true });
  var Zl = er(), Vl = re(), MX = ue(), DX = { message: ({ params: { missingProperty: e } }) => Vl.str`must have required property '${e}'`, params: ({ params: { missingProperty: e } }) => Vl._`{missingProperty: ${e}}` }, NX = { keyword: "required", type: "object", schemaType: "array", $data: true, error: DX, code(e) {
    let { gen: t, schema: r, schemaCode: o, data: n, $data: i, it: s } = e, { opts: a } = s;
    if (!i && r.length === 0) return;
    let c = r.length >= a.loopRequired;
    if (s.allErrors) u();
    else d();
    if (a.strictRequired) {
      let m = e.parentSchema.properties, { definedProperties: g } = e.it;
      for (let h of r) if ((m === null || m === void 0 ? void 0 : m[h]) === void 0 && !g.has(h)) {
        let y = s.schemaEnv.baseId + s.errSchemaPath, v = `required property "${h}" is not defined at "${y}" (strictRequired)`;
        (0, MX.checkStrictMode)(s, v, s.opts.strictRequired);
      }
    }
    function u() {
      if (c || i) e.block$data(Vl.nil, p);
      else for (let m of r) (0, Zl.checkReportMissingProp)(e, m);
    }
    function d() {
      let m = t.let("missing");
      if (c || i) {
        let g = t.let("valid", true);
        e.block$data(g, () => f(m, g)), e.ok(g);
      } else t.if((0, Zl.checkMissingProp)(e, r, m)), (0, Zl.reportMissingProp)(e, m), t.else();
    }
    function p() {
      t.forOf("prop", o, (m) => {
        e.setParams({ missingProperty: m }), t.if((0, Zl.noPropertyInData)(t, n, m, a.ownProperties), () => e.error());
      });
    }
    function f(m, g) {
      e.setParams({ missingProperty: m }), t.forOf(m, o, () => {
        t.assign(g, (0, Zl.propertyInData)(t, n, m, a.ownProperties)), t.if((0, Vl.not)(g), () => {
          e.error(), t.break();
        });
      }, Vl.nil);
    }
  } };
  IM.default = NX;
});
var OM = k(($M) => {
  Object.defineProperty($M, "__esModule", { value: true });
  var Wl = re(), UX = { message({ keyword: e, schemaCode: t }) {
    let r = e === "maxItems" ? "more" : "fewer";
    return Wl.str`must NOT have ${r} than ${t} items`;
  }, params: ({ schemaCode: e }) => Wl._`{limit: ${e}}` }, zX = { keyword: ["maxItems", "minItems"], type: "array", schemaType: "number", $data: true, error: UX, code(e) {
    let { keyword: t, data: r, schemaCode: o } = e, n = t === "maxItems" ? Wl.operators.GT : Wl.operators.LT;
    e.fail$data(Wl._`${r}.length ${n} ${o}`);
  } };
  $M.default = zX;
});
var Vm = k((CM) => {
  Object.defineProperty(CM, "__esModule", { value: true });
  var AM = zS();
  AM.code = 'require("ajv/dist/runtime/equal").default';
  CM.default = AM;
});
var DM = k((MM) => {
  Object.defineProperty(MM, "__esModule", { value: true });
  var rx = Cl(), nt = re(), BX = ue(), HX = Vm(), qX = { message: ({ params: { i: e, j: t } }) => nt.str`must NOT have duplicate items (items ## ${t} and ${e} are identical)`, params: ({ params: { i: e, j: t } }) => nt._`{i: ${e}, j: ${t}}` }, ZX = { keyword: "uniqueItems", type: "array", schemaType: "boolean", $data: true, error: qX, code(e) {
    let { gen: t, data: r, $data: o, schema: n, parentSchema: i, schemaCode: s, it: a } = e;
    if (!o && !n) return;
    let c = t.let("valid"), u = i.items ? (0, rx.getSchemaTypes)(i.items) : [];
    e.block$data(c, d, nt._`${s} === false`), e.ok(c);
    function d() {
      let g = t.let("i", nt._`${r}.length`), h = t.let("j");
      e.setParams({ i: g, j: h }), t.assign(c, true), t.if(nt._`${g} > 1`, () => (p() ? f : m)(g, h));
    }
    function p() {
      return u.length > 0 && !u.some((g) => g === "object" || g === "array");
    }
    function f(g, h) {
      let y = t.name("item"), v = (0, rx.checkDataTypes)(u, y, a.opts.strictNumbers, rx.DataType.Wrong), x = t.const("indices", nt._`{}`);
      t.for(nt._`;${g}--;`, () => {
        if (t.let(y, nt._`${r}[${g}]`), t.if(v, nt._`continue`), u.length > 1) t.if(nt._`typeof ${y} == "string"`, nt._`${y} += "_"`);
        t.if(nt._`typeof ${x}[${y}] == "number"`, () => {
          t.assign(h, nt._`${x}[${y}]`), e.error(), t.assign(c, false).break();
        }).code(nt._`${x}[${y}] = ${g}`);
      });
    }
    function m(g, h) {
      let y = (0, BX.useFunc)(t, HX.default), v = t.name("outer");
      t.label(v).for(nt._`;${g}--;`, () => t.for(nt._`${h} = ${g}; ${h}--;`, () => t.if(nt._`${y}(${r}[${g}], ${r}[${h}])`, () => {
        e.error(), t.assign(c, false).break(v);
      })));
    }
  } };
  MM.default = ZX;
});
var jM = k((NM) => {
  Object.defineProperty(NM, "__esModule", { value: true });
  var nx = re(), WX = ue(), KX = Vm(), GX = { message: "must be equal to constant", params: ({ schemaCode: e }) => nx._`{allowedValue: ${e}}` }, JX = { keyword: "const", $data: true, error: GX, code(e) {
    let { gen: t, data: r, $data: o, schemaCode: n, schema: i } = e;
    if (o || i && typeof i == "object") e.fail$data(nx._`!${(0, WX.useFunc)(t, KX.default)}(${r}, ${n})`);
    else e.fail(nx._`${i} !== ${r}`);
  } };
  NM.default = JX;
});
var zM = k((UM) => {
  Object.defineProperty(UM, "__esModule", { value: true });
  var Kl = re(), YX = ue(), QX = Vm(), eY = { message: "must be equal to one of the allowed values", params: ({ schemaCode: e }) => Kl._`{allowedValues: ${e}}` }, tY = { keyword: "enum", schemaType: "array", $data: true, error: eY, code(e) {
    let { gen: t, data: r, $data: o, schema: n, schemaCode: i, it: s } = e;
    if (!o && n.length === 0) throw Error("enum must have non-empty array");
    let a = n.length >= s.opts.loopEnum, c, u = () => c !== null && c !== void 0 ? c : c = (0, YX.useFunc)(t, QX.default), d;
    if (a || o) d = t.let("valid"), e.block$data(d, p);
    else {
      if (!Array.isArray(n)) throw Error("ajv implementation error");
      let m = t.const("vSchema", i);
      d = (0, Kl.or)(...n.map((g, h) => f(m, h)));
    }
    e.pass(d);
    function p() {
      t.assign(d, false), t.forOf("v", i, (m) => t.if(Kl._`${u()}(${r}, ${m})`, () => t.assign(d, true).break()));
    }
    function f(m, g) {
      let h = n[g];
      return typeof h === "object" && h !== null ? Kl._`${u()}(${r}, ${m}[${g}])` : Kl._`${r} === ${h}`;
    }
  } };
  UM.default = tY;
});
var FM = k((LM) => {
  Object.defineProperty(LM, "__esModule", { value: true });
  var nY = hM(), oY = bM(), iY = wM(), sY = EM(), aY = PM(), cY = RM(), lY = OM(), uY = DM(), dY = jM(), pY = zM(), fY = [nY.default, oY.default, iY.default, sY.default, aY.default, cY.default, lY.default, uY.default, { keyword: "type", schemaType: ["string", "array"] }, { keyword: "nullable", schemaType: "boolean" }, dY.default, pY.default];
  LM.default = fY;
});
var ix = k((HM) => {
  Object.defineProperty(HM, "__esModule", { value: true });
  HM.validateAdditionalItems = void 0;
  var Fo = re(), ox = ue(), gY = { message: ({ params: { len: e } }) => Fo.str`must NOT have more than ${e} items`, params: ({ params: { len: e } }) => Fo._`{limit: ${e}}` }, hY = { keyword: "additionalItems", type: "array", schemaType: ["boolean", "object"], before: "uniqueItems", error: gY, code(e) {
    let { parentSchema: t, it: r } = e, { items: o } = t;
    if (!Array.isArray(o)) {
      (0, ox.checkStrictMode)(r, '"additionalItems" is ignored when "items" is not an array of schemas');
      return;
    }
    BM(e, o);
  } };
  function BM(e, t) {
    let { gen: r, schema: o, data: n, keyword: i, it: s } = e;
    s.items = true;
    let a = r.const("len", Fo._`${n}.length`);
    if (o === false) e.setParams({ len: t.length }), e.pass(Fo._`${a} <= ${t.length}`);
    else if (typeof o == "object" && !(0, ox.alwaysValidSchema)(s, o)) {
      let u = r.var("valid", Fo._`${a} <= ${t.length}`);
      r.if((0, Fo.not)(u), () => c(u)), e.ok(u);
    }
    function c(u) {
      r.forRange("i", t.length, a, (d) => {
        if (e.subschema({ keyword: i, dataProp: d, dataPropType: ox.Type.Num }, u), !s.allErrors) r.if((0, Fo.not)(u), () => r.break());
      });
    }
  }
  HM.validateAdditionalItems = BM;
  HM.default = hY;
});
var sx = k((WM) => {
  Object.defineProperty(WM, "__esModule", { value: true });
  WM.validateTuple = void 0;
  var ZM = re(), Wm = ue(), bY = er(), _Y = { keyword: "items", type: "array", schemaType: ["object", "array", "boolean"], before: "uniqueItems", code(e) {
    let { schema: t, it: r } = e;
    if (Array.isArray(t)) return VM(e, "additionalItems", t);
    if (r.items = true, (0, Wm.alwaysValidSchema)(r, t)) return;
    e.ok((0, bY.validateArray)(e));
  } };
  function VM(e, t, r = e.schema) {
    let { gen: o, parentSchema: n, data: i, keyword: s, it: a } = e;
    if (d(n), a.opts.unevaluated && r.length && a.items !== true) a.items = Wm.mergeEvaluated.items(o, r.length, a.items);
    let c = o.name("valid"), u = o.const("len", ZM._`${i}.length`);
    r.forEach((p, f) => {
      if ((0, Wm.alwaysValidSchema)(a, p)) return;
      o.if(ZM._`${u} > ${f}`, () => e.subschema({ keyword: s, schemaProp: f, dataProp: f }, c)), e.ok(c);
    });
    function d(p) {
      let { opts: f, errSchemaPath: m } = a, g = r.length, h = g === p.minItems && (g === p.maxItems || p[t] === false);
      if (f.strictTuples && !h) {
        let y = `"${s}" is ${g}-tuple, but minItems or maxItems/${t} are not specified or different at path "${m}"`;
        (0, Wm.checkStrictMode)(a, y, f.strictTuples);
      }
    }
  }
  WM.validateTuple = VM;
  WM.default = _Y;
});
var JM = k((GM) => {
  Object.defineProperty(GM, "__esModule", { value: true });
  var SY = sx(), xY = { keyword: "prefixItems", type: "array", schemaType: ["array"], before: "uniqueItems", code: (e) => (0, SY.validateTuple)(e, "items") };
  GM.default = xY;
});
var QM = k((YM) => {
  Object.defineProperty(YM, "__esModule", { value: true });
  var XM = re(), kY = ue(), EY = er(), TY = ix(), PY = { message: ({ params: { len: e } }) => XM.str`must NOT have more than ${e} items`, params: ({ params: { len: e } }) => XM._`{limit: ${e}}` }, IY = { keyword: "items", type: "array", schemaType: ["object", "boolean"], before: "uniqueItems", error: PY, code(e) {
    let { schema: t, parentSchema: r, it: o } = e, { prefixItems: n } = r;
    if (o.items = true, (0, kY.alwaysValidSchema)(o, t)) return;
    if (n) (0, TY.validateAdditionalItems)(e, n);
    else e.ok((0, EY.validateArray)(e));
  } };
  YM.default = IY;
});
var tD = k((eD) => {
  Object.defineProperty(eD, "__esModule", { value: true });
  var tr = re(), Km = ue(), $Y = { message: ({ params: { min: e, max: t } }) => t === void 0 ? tr.str`must contain at least ${e} valid item(s)` : tr.str`must contain at least ${e} and no more than ${t} valid item(s)`, params: ({ params: { min: e, max: t } }) => t === void 0 ? tr._`{minContains: ${e}}` : tr._`{minContains: ${e}, maxContains: ${t}}` }, OY = { keyword: "contains", type: "array", schemaType: ["object", "boolean"], before: "uniqueItems", trackErrors: true, error: $Y, code(e) {
    let { gen: t, schema: r, parentSchema: o, data: n, it: i } = e, s, a, { minContains: c, maxContains: u } = o;
    if (i.opts.next) s = c === void 0 ? 1 : c, a = u;
    else s = 1;
    let d = t.const("len", tr._`${n}.length`);
    if (e.setParams({ min: s, max: a }), a === void 0 && s === 0) {
      (0, Km.checkStrictMode)(i, '"minContains" == 0 without "maxContains": "contains" keyword ignored');
      return;
    }
    if (a !== void 0 && s > a) {
      (0, Km.checkStrictMode)(i, '"minContains" > "maxContains" is always invalid'), e.fail();
      return;
    }
    if ((0, Km.alwaysValidSchema)(i, r)) {
      let h = tr._`${d} >= ${s}`;
      if (a !== void 0) h = tr._`${h} && ${d} <= ${a}`;
      e.pass(h);
      return;
    }
    i.items = true;
    let p = t.name("valid");
    if (a === void 0 && s === 1) m(p, () => t.if(p, () => t.break()));
    else if (s === 0) {
      if (t.let(p, true), a !== void 0) t.if(tr._`${n}.length > 0`, f);
    } else t.let(p, false), f();
    e.result(p, () => e.reset());
    function f() {
      let h = t.name("_valid"), y = t.let("count", 0);
      m(h, () => t.if(h, () => g(y)));
    }
    function m(h, y) {
      t.forRange("i", 0, d, (v) => {
        e.subschema({ keyword: "contains", dataProp: v, dataPropType: Km.Type.Num, compositeRule: true }, h), y();
      });
    }
    function g(h) {
      if (t.code(tr._`${h}++`), a === void 0) t.if(tr._`${h} >= ${s}`, () => t.assign(p, true).break());
      else if (t.if(tr._`${h} > ${a}`, () => t.assign(p, false).break()), s === 1) t.assign(p, true);
      else t.if(tr._`${h} >= ${s}`, () => t.assign(p, true));
    }
  } };
  eD.default = OY;
});
var aD = k((oD) => {
  Object.defineProperty(oD, "__esModule", { value: true });
  oD.validateSchemaDeps = oD.validatePropertyDeps = oD.error = void 0;
  var ax = re(), CY = ue(), Gl = er();
  oD.error = { message: ({ params: { property: e, depsCount: t, deps: r } }) => {
    let o = t === 1 ? "property" : "properties";
    return ax.str`must have ${o} ${r} when property ${e} is present`;
  }, params: ({ params: { property: e, depsCount: t, deps: r, missingProperty: o } }) => ax._`{property: ${e},
    missingProperty: ${o},
    depsCount: ${t},
    deps: ${r}}` };
  var MY = { keyword: "dependencies", type: "object", schemaType: "object", error: oD.error, code(e) {
    let [t, r] = DY(e);
    rD(e, t), nD(e, r);
  } };
  function DY({ schema: e }) {
    let t = {}, r = {};
    for (let o in e) {
      if (o === "__proto__") continue;
      let n = Array.isArray(e[o]) ? t : r;
      n[o] = e[o];
    }
    return [t, r];
  }
  function rD(e, t = e.schema) {
    let { gen: r, data: o, it: n } = e;
    if (Object.keys(t).length === 0) return;
    let i = r.let("missing");
    for (let s in t) {
      let a = t[s];
      if (a.length === 0) continue;
      let c = (0, Gl.propertyInData)(r, o, s, n.opts.ownProperties);
      if (e.setParams({ property: s, depsCount: a.length, deps: a.join(", ") }), n.allErrors) r.if(c, () => {
        for (let u of a) (0, Gl.checkReportMissingProp)(e, u);
      });
      else r.if(ax._`${c} && (${(0, Gl.checkMissingProp)(e, a, i)})`), (0, Gl.reportMissingProp)(e, i), r.else();
    }
  }
  oD.validatePropertyDeps = rD;
  function nD(e, t = e.schema) {
    let { gen: r, data: o, keyword: n, it: i } = e, s = r.name("valid");
    for (let a in t) {
      if ((0, CY.alwaysValidSchema)(i, t[a])) continue;
      r.if((0, Gl.propertyInData)(r, o, a, i.opts.ownProperties), () => {
        let c = e.subschema({ keyword: n, schemaProp: a }, s);
        e.mergeValidEvaluated(c, s);
      }, () => r.var(s, true)), e.ok(s);
    }
  }
  oD.validateSchemaDeps = nD;
  oD.default = MY;
});
var uD = k((lD) => {
  Object.defineProperty(lD, "__esModule", { value: true });
  var cD = re(), UY = ue(), zY = { message: "property name must be valid", params: ({ params: e }) => cD._`{propertyName: ${e.propertyName}}` }, LY = { keyword: "propertyNames", type: "object", schemaType: ["object", "boolean"], error: zY, code(e) {
    let { gen: t, schema: r, data: o, it: n } = e;
    if ((0, UY.alwaysValidSchema)(n, r)) return;
    let i = t.name("valid");
    t.forIn("key", o, (s) => {
      e.setParams({ propertyName: s }), e.subschema({ keyword: "propertyNames", data: s, dataTypes: ["string"], propertyName: s, compositeRule: true }, i), t.if((0, cD.not)(i), () => {
        if (e.error(true), !n.allErrors) t.break();
      });
    }), e.ok(i);
  } };
  lD.default = LY;
});
var cx = k((dD) => {
  Object.defineProperty(dD, "__esModule", { value: true });
  var Gm = er(), yr = re(), BY = tn(), Jm = ue(), HY = { message: "must NOT have additional properties", params: ({ params: e }) => yr._`{additionalProperty: ${e.additionalProperty}}` }, qY = { keyword: "additionalProperties", type: ["object"], schemaType: ["boolean", "object"], allowUndefined: true, trackErrors: true, error: HY, code(e) {
    let { gen: t, schema: r, parentSchema: o, data: n, errsCount: i, it: s } = e;
    if (!i) throw Error("ajv implementation error");
    let { allErrors: a, opts: c } = s;
    if (s.props = true, c.removeAdditional !== "all" && (0, Jm.alwaysValidSchema)(s, r)) return;
    let u = (0, Gm.allSchemaProperties)(o.properties), d = (0, Gm.allSchemaProperties)(o.patternProperties);
    p(), e.ok(yr._`${i} === ${BY.default.errors}`);
    function p() {
      t.forIn("key", n, (y) => {
        if (!u.length && !d.length) g(y);
        else t.if(f(y), () => g(y));
      });
    }
    function f(y) {
      let v;
      if (u.length > 8) {
        let x = (0, Jm.schemaRefOrVal)(s, o.properties, "properties");
        v = (0, Gm.isOwnProperty)(t, x, y);
      } else if (u.length) v = (0, yr.or)(...u.map((x) => yr._`${y} === ${x}`));
      else v = yr.nil;
      if (d.length) v = (0, yr.or)(v, ...d.map((x) => yr._`${(0, Gm.usePattern)(e, x)}.test(${y})`));
      return (0, yr.not)(v);
    }
    function m(y) {
      t.code(yr._`delete ${n}[${y}]`);
    }
    function g(y) {
      if (c.removeAdditional === "all" || c.removeAdditional && r === false) {
        m(y);
        return;
      }
      if (r === false) {
        if (e.setParams({ additionalProperty: y }), e.error(), !a) t.break();
        return;
      }
      if (typeof r == "object" && !(0, Jm.alwaysValidSchema)(s, r)) {
        let v = t.name("valid");
        if (c.removeAdditional === "failing") h(y, v, false), t.if((0, yr.not)(v), () => {
          e.reset(), m(y);
        });
        else if (h(y, v), !a) t.if((0, yr.not)(v), () => t.break());
      }
    }
    function h(y, v, x) {
      let w = { keyword: "additionalProperties", dataProp: y, dataPropType: Jm.Type.Str };
      if (x === false) Object.assign(w, { compositeRule: true, createErrors: false, allErrors: false });
      e.subschema(w, v);
    }
  } };
  dD.default = qY;
});
var gD = k((mD) => {
  Object.defineProperty(mD, "__esModule", { value: true });
  var VY = jl(), pD = er(), lx = ue(), fD = cx(), WY = { keyword: "properties", type: "object", schemaType: "object", code(e) {
    let { gen: t, schema: r, parentSchema: o, data: n, it: i } = e;
    if (i.opts.removeAdditional === "all" && o.additionalProperties === void 0) fD.default.code(new VY.KeywordCxt(i, fD.default, "additionalProperties"));
    let s = (0, pD.allSchemaProperties)(r);
    for (let p of s) i.definedProperties.add(p);
    if (i.opts.unevaluated && s.length && i.props !== true) i.props = lx.mergeEvaluated.props(t, (0, lx.toHash)(s), i.props);
    let a = s.filter((p) => !(0, lx.alwaysValidSchema)(i, r[p]));
    if (a.length === 0) return;
    let c = t.name("valid");
    for (let p of a) {
      if (u(p)) d(p);
      else {
        if (t.if((0, pD.propertyInData)(t, n, p, i.opts.ownProperties)), d(p), !i.allErrors) t.else().var(c, true);
        t.endIf();
      }
      e.it.definedProperties.add(p), e.ok(c);
    }
    function u(p) {
      return i.opts.useDefaults && !i.compositeRule && r[p].default !== void 0;
    }
    function d(p) {
      e.subschema({ keyword: "properties", schemaProp: p, dataProp: p }, c);
    }
  } };
  mD.default = WY;
});
var vD = k((_D) => {
  Object.defineProperty(_D, "__esModule", { value: true });
  var hD = er(), Xm = re(), yD = ue(), bD = ue(), GY = { keyword: "patternProperties", type: "object", schemaType: "object", code(e) {
    let { gen: t, schema: r, data: o, parentSchema: n, it: i } = e, { opts: s } = i, a = (0, hD.allSchemaProperties)(r), c = a.filter((h) => (0, yD.alwaysValidSchema)(i, r[h]));
    if (a.length === 0 || c.length === a.length && (!i.opts.unevaluated || i.props === true)) return;
    let u = s.strictSchema && !s.allowMatchingProperties && n.properties, d = t.name("valid");
    if (i.props !== true && !(i.props instanceof Xm.Name)) i.props = (0, bD.evaluatedPropsToName)(t, i.props);
    let { props: p } = i;
    f();
    function f() {
      for (let h of a) {
        if (u) m(h);
        if (i.allErrors) g(h);
        else t.var(d, true), g(h), t.if(d);
      }
    }
    function m(h) {
      for (let y in u) if (new RegExp(h).test(y)) (0, yD.checkStrictMode)(i, `property ${y} matches pattern ${h} (use allowMatchingProperties)`);
    }
    function g(h) {
      t.forIn("key", o, (y) => {
        t.if(Xm._`${(0, hD.usePattern)(e, h)}.test(${y})`, () => {
          let v = c.includes(h);
          if (!v) e.subschema({ keyword: "patternProperties", schemaProp: h, dataProp: y, dataPropType: bD.Type.Str }, d);
          if (i.opts.unevaluated && p !== true) t.assign(Xm._`${p}[${y}]`, true);
          else if (!v && !i.allErrors) t.if((0, Xm.not)(d), () => t.break());
        });
      });
    }
  } };
  _D.default = GY;
});
var xD = k((SD) => {
  Object.defineProperty(SD, "__esModule", { value: true });
  var XY = ue(), YY = { keyword: "not", schemaType: ["object", "boolean"], trackErrors: true, code(e) {
    let { gen: t, schema: r, it: o } = e;
    if ((0, XY.alwaysValidSchema)(o, r)) {
      e.fail();
      return;
    }
    let n = t.name("valid");
    e.subschema({ keyword: "not", compositeRule: true, createErrors: false, allErrors: false }, n), e.failResult(n, () => e.reset(), () => e.error());
  }, error: { message: "must NOT be valid" } };
  SD.default = YY;
});
var kD = k((wD) => {
  Object.defineProperty(wD, "__esModule", { value: true });
  var e7 = er(), t7 = { keyword: "anyOf", schemaType: "array", trackErrors: true, code: e7.validateUnion, error: { message: "must match a schema in anyOf" } };
  wD.default = t7;
});
var TD = k((ED) => {
  Object.defineProperty(ED, "__esModule", { value: true });
  var Ym = re(), n7 = ue(), o7 = { message: "must match exactly one schema in oneOf", params: ({ params: e }) => Ym._`{passingSchemas: ${e.passing}}` }, i7 = { keyword: "oneOf", schemaType: "array", trackErrors: true, error: o7, code(e) {
    let { gen: t, schema: r, parentSchema: o, it: n } = e;
    if (!Array.isArray(r)) throw Error("ajv implementation error");
    if (n.opts.discriminator && o.discriminator) return;
    let i = r, s = t.let("valid", false), a = t.let("passing", null), c = t.name("_valid");
    e.setParams({ passing: a }), t.block(u), e.result(s, () => e.reset(), () => e.error(true));
    function u() {
      i.forEach((d, p) => {
        let f;
        if ((0, n7.alwaysValidSchema)(n, d)) t.var(c, true);
        else f = e.subschema({ keyword: "oneOf", schemaProp: p, compositeRule: true }, c);
        if (p > 0) t.if(Ym._`${c} && ${s}`).assign(s, false).assign(a, Ym._`[${a}, ${p}]`).else();
        t.if(c, () => {
          if (t.assign(s, true), t.assign(a, p), f) e.mergeEvaluated(f, Ym.Name);
        });
      });
    }
  } };
  ED.default = i7;
});
var ID = k((PD) => {
  Object.defineProperty(PD, "__esModule", { value: true });
  var a7 = ue(), c7 = { keyword: "allOf", schemaType: "array", code(e) {
    let { gen: t, schema: r, it: o } = e;
    if (!Array.isArray(r)) throw Error("ajv implementation error");
    let n = t.name("valid");
    r.forEach((i, s) => {
      if ((0, a7.alwaysValidSchema)(o, i)) return;
      let a = e.subschema({ keyword: "allOf", schemaProp: s }, n);
      e.ok(n), e.mergeEvaluated(a);
    });
  } };
  PD.default = c7;
});
var AD = k((OD) => {
  Object.defineProperty(OD, "__esModule", { value: true });
  var Qm = re(), $D = ue(), u7 = { message: ({ params: e }) => Qm.str`must match "${e.ifClause}" schema`, params: ({ params: e }) => Qm._`{failingKeyword: ${e.ifClause}}` }, d7 = { keyword: "if", schemaType: ["object", "boolean"], trackErrors: true, error: u7, code(e) {
    let { gen: t, parentSchema: r, it: o } = e;
    if (r.then === void 0 && r.else === void 0) (0, $D.checkStrictMode)(o, '"if" without "then" and "else" is ignored');
    let n = RD(o, "then"), i = RD(o, "else");
    if (!n && !i) return;
    let s = t.let("valid", true), a = t.name("_valid");
    if (c(), e.reset(), n && i) {
      let d = t.let("ifClause");
      e.setParams({ ifClause: d }), t.if(a, u("then", d), u("else", d));
    } else if (n) t.if(a, u("then"));
    else t.if((0, Qm.not)(a), u("else"));
    e.pass(s, () => e.error(true));
    function c() {
      let d = e.subschema({ keyword: "if", compositeRule: true, createErrors: false, allErrors: false }, a);
      e.mergeEvaluated(d);
    }
    function u(d, p) {
      return () => {
        let f = e.subschema({ keyword: d }, a);
        if (t.assign(s, a), e.mergeValidEvaluated(f, s), p) t.assign(p, Qm._`${d}`);
        else e.setParams({ ifClause: d });
      };
    }
  } };
  function RD(e, t) {
    let r = e.schema[t];
    return r !== void 0 && !(0, $D.alwaysValidSchema)(e, r);
  }
  OD.default = d7;
});
var MD = k((CD) => {
  Object.defineProperty(CD, "__esModule", { value: true });
  var f7 = ue(), m7 = { keyword: ["then", "else"], schemaType: ["object", "boolean"], code({ keyword: e, parentSchema: t, it: r }) {
    if (t.if === void 0) (0, f7.checkStrictMode)(r, `"${e}" without "if" is ignored`);
  } };
  CD.default = m7;
});
var ND = k((DD) => {
  Object.defineProperty(DD, "__esModule", { value: true });
  var h7 = ix(), y7 = JM(), b7 = sx(), _7 = QM(), v7 = tD(), S7 = aD(), x7 = uD(), w7 = cx(), k7 = gD(), E7 = vD(), T7 = xD(), P7 = kD(), I7 = TD(), R7 = ID(), $7 = AD(), O7 = MD();
  function A7(e = false) {
    let t = [T7.default, P7.default, I7.default, R7.default, $7.default, O7.default, x7.default, w7.default, S7.default, k7.default, E7.default];
    if (e) t.push(y7.default, _7.default);
    else t.push(h7.default, b7.default);
    return t.push(v7.default), t;
  }
  DD.default = A7;
});
var UD = k((jD) => {
  Object.defineProperty(jD, "__esModule", { value: true });
  var qe = re(), M7 = { message: ({ schemaCode: e }) => qe.str`must match format "${e}"`, params: ({ schemaCode: e }) => qe._`{format: ${e}}` }, D7 = { keyword: "format", type: ["number", "string"], schemaType: "string", $data: true, error: M7, code(e, t) {
    let { gen: r, data: o, $data: n, schema: i, schemaCode: s, it: a } = e, { opts: c, errSchemaPath: u, schemaEnv: d, self: p } = a;
    if (!c.validateFormats) return;
    if (n) f();
    else m();
    function f() {
      let g = r.scopeValue("formats", { ref: p.formats, code: c.code.formats }), h = r.const("fDef", qe._`${g}[${s}]`), y = r.let("fType"), v = r.let("format");
      r.if(qe._`typeof ${h} == "object" && !(${h} instanceof RegExp)`, () => r.assign(y, qe._`${h}.type || "string"`).assign(v, qe._`${h}.validate`), () => r.assign(y, qe._`"string"`).assign(v, h)), e.fail$data((0, qe.or)(x(), w()));
      function x() {
        if (c.strictSchema === false) return qe.nil;
        return qe._`${s} && !${v}`;
      }
      function w() {
        let A = d.$async ? qe._`(${h}.async ? await ${v}(${o}) : ${v}(${o}))` : qe._`${v}(${o})`, U = qe._`(typeof ${v} == "function" ? ${A} : ${v}.test(${o}))`;
        return qe._`${v} && ${v} !== true && ${y} === ${t} && !${U}`;
      }
    }
    function m() {
      let g = p.formats[i];
      if (!g) {
        x();
        return;
      }
      if (g === true) return;
      let [h, y, v] = w(g);
      if (h === t) e.pass(A());
      function x() {
        if (c.strictSchema === false) {
          p.logger.warn(U());
          return;
        }
        throw Error(U());
        function U() {
          return `unknown format "${i}" ignored in schema at path "${u}"`;
        }
      }
      function w(U) {
        let se = U instanceof RegExp ? (0, qe.regexpCode)(U) : c.code.formats ? qe._`${c.code.formats}${(0, qe.getProperty)(i)}` : void 0, Le = r.scopeValue("formats", { key: i, ref: U, code: se });
        if (typeof U == "object" && !(U instanceof RegExp)) return [U.type || "string", U.validate, qe._`${Le}.validate`];
        return ["string", U, Le];
      }
      function A() {
        if (typeof g == "object" && !(g instanceof RegExp) && g.async) {
          if (!d.$async) throw Error("async format in sync schema");
          return qe._`await ${v}(${o})`;
        }
        return typeof y == "function" ? qe._`${v}(${o})` : qe._`${v}.test(${o})`;
      }
    }
  } };
  jD.default = D7;
});
var LD = k((zD) => {
  Object.defineProperty(zD, "__esModule", { value: true });
  var j7 = UD(), U7 = [j7.default];
  zD.default = U7;
});
var HD = k((FD) => {
  Object.defineProperty(FD, "__esModule", { value: true });
  FD.contentVocabulary = FD.metadataVocabulary = void 0;
  FD.metadataVocabulary = ["title", "description", "default", "deprecated", "readOnly", "writeOnly", "examples"];
  FD.contentVocabulary = ["contentMediaType", "contentEncoding", "contentSchema"];
});
var VD = k((ZD) => {
  Object.defineProperty(ZD, "__esModule", { value: true });
  var F7 = mM(), B7 = FM(), H7 = ND(), q7 = LD(), qD = HD(), Z7 = [F7.default, B7.default, (0, H7.default)(), q7.default, qD.metadataVocabulary, qD.contentVocabulary];
  ZD.default = Z7;
});
var JD = k((KD) => {
  Object.defineProperty(KD, "__esModule", { value: true });
  KD.DiscrError = void 0;
  var WD;
  (function(e) {
    e.Tag = "tag", e.Mapping = "mapping";
  })(WD || (KD.DiscrError = WD = {}));
});
var QD = k((YD) => {
  Object.defineProperty(YD, "__esModule", { value: true });
  var Es = re(), ux = JD(), XD = Nm(), W7 = Ul(), K7 = ue(), G7 = { message: ({ params: { discrError: e, tagName: t } }) => e === ux.DiscrError.Tag ? `tag "${t}" must be string` : `value of tag "${t}" must be in oneOf`, params: ({ params: { discrError: e, tag: t, tagName: r } }) => Es._`{error: ${e}, tag: ${r}, tagValue: ${t}}` }, J7 = { keyword: "discriminator", type: "object", schemaType: "object", error: G7, code(e) {
    let { gen: t, data: r, schema: o, parentSchema: n, it: i } = e, { oneOf: s } = n;
    if (!i.opts.discriminator) throw Error("discriminator: requires discriminator option");
    let a = o.propertyName;
    if (typeof a != "string") throw Error("discriminator: requires propertyName");
    if (o.mapping) throw Error("discriminator: mapping is not supported");
    if (!s) throw Error("discriminator: requires oneOf keyword");
    let c = t.let("valid", false), u = t.const("tag", Es._`${r}${(0, Es.getProperty)(a)}`);
    t.if(Es._`typeof ${u} == "string"`, () => d(), () => e.error(false, { discrError: ux.DiscrError.Tag, tag: u, tagName: a })), e.ok(c);
    function d() {
      let m = f();
      t.if(false);
      for (let g in m) t.elseIf(Es._`${u} === ${g}`), t.assign(c, p(m[g]));
      t.else(), e.error(false, { discrError: ux.DiscrError.Mapping, tag: u, tagName: a }), t.endIf();
    }
    function p(m) {
      let g = t.name("valid"), h = e.subschema({ keyword: "oneOf", schemaProp: m }, g);
      return e.mergeEvaluated(h, Es.Name), g;
    }
    function f() {
      var m;
      let g = {}, h = v(n), y = true;
      for (let A = 0; A < s.length; A++) {
        let U = s[A];
        if ((U === null || U === void 0 ? void 0 : U.$ref) && !(0, K7.schemaHasRulesButRef)(U, i.self.RULES)) {
          let Le = U.$ref;
          if (U = XD.resolveRef.call(i.self, i.schemaEnv.root, i.baseId, Le), U instanceof XD.SchemaEnv) U = U.schema;
          if (U === void 0) throw new W7.default(i.opts.uriResolver, i.baseId, Le);
        }
        let se = (m = U === null || U === void 0 ? void 0 : U.properties) === null || m === void 0 ? void 0 : m[a];
        if (typeof se != "object") throw Error(`discriminator: oneOf subschemas (or referenced schemas) must have "properties/${a}"`);
        y = y && (h || v(U)), x(se, A);
      }
      if (!y) throw Error(`discriminator: "${a}" must be required`);
      return g;
      function v({ required: A }) {
        return Array.isArray(A) && A.includes(a);
      }
      function x(A, U) {
        if (A.const) w(A.const, U);
        else if (A.enum) for (let se of A.enum) w(se, U);
        else throw Error(`discriminator: "properties/${a}" must have "const" or "enum"`);
      }
      function w(A, U) {
        if (typeof A != "string" || A in g) throw Error(`discriminator: "${a}" values must be unique strings`);
        g[A] = U;
      }
    }
  } };
  YD.default = J7;
});
var eN = k((MTe, Y7) => {
  Y7.exports = { $schema: "http://json-schema.org/draft-07/schema#", $id: "http://json-schema.org/draft-07/schema#", title: "Core schema meta-schema", definitions: { schemaArray: { type: "array", minItems: 1, items: { $ref: "#" } }, nonNegativeInteger: { type: "integer", minimum: 0 }, nonNegativeIntegerDefault0: { allOf: [{ $ref: "#/definitions/nonNegativeInteger" }, { default: 0 }] }, simpleTypes: { enum: ["array", "boolean", "integer", "null", "number", "object", "string"] }, stringArray: { type: "array", items: { type: "string" }, uniqueItems: true, default: [] } }, type: ["object", "boolean"], properties: { $id: { type: "string", format: "uri-reference" }, $schema: { type: "string", format: "uri" }, $ref: { type: "string", format: "uri-reference" }, $comment: { type: "string" }, title: { type: "string" }, description: { type: "string" }, default: true, readOnly: { type: "boolean", default: false }, examples: { type: "array", items: true }, multipleOf: { type: "number", exclusiveMinimum: 0 }, maximum: { type: "number" }, exclusiveMaximum: { type: "number" }, minimum: { type: "number" }, exclusiveMinimum: { type: "number" }, maxLength: { $ref: "#/definitions/nonNegativeInteger" }, minLength: { $ref: "#/definitions/nonNegativeIntegerDefault0" }, pattern: { type: "string", format: "regex" }, additionalItems: { $ref: "#" }, items: { anyOf: [{ $ref: "#" }, { $ref: "#/definitions/schemaArray" }], default: true }, maxItems: { $ref: "#/definitions/nonNegativeInteger" }, minItems: { $ref: "#/definitions/nonNegativeIntegerDefault0" }, uniqueItems: { type: "boolean", default: false }, contains: { $ref: "#" }, maxProperties: { $ref: "#/definitions/nonNegativeInteger" }, minProperties: { $ref: "#/definitions/nonNegativeIntegerDefault0" }, required: { $ref: "#/definitions/stringArray" }, additionalProperties: { $ref: "#" }, definitions: { type: "object", additionalProperties: { $ref: "#" }, default: {} }, properties: { type: "object", additionalProperties: { $ref: "#" }, default: {} }, patternProperties: { type: "object", additionalProperties: { $ref: "#" }, propertyNames: { format: "regex" }, default: {} }, dependencies: { type: "object", additionalProperties: { anyOf: [{ $ref: "#" }, { $ref: "#/definitions/stringArray" }] } }, propertyNames: { $ref: "#" }, const: true, enum: { type: "array", items: true, minItems: 1, uniqueItems: true }, type: { anyOf: [{ $ref: "#/definitions/simpleTypes" }, { type: "array", items: { $ref: "#/definitions/simpleTypes" }, minItems: 1, uniqueItems: true }] }, format: { type: "string" }, contentMediaType: { type: "string" }, contentEncoding: { type: "string" }, if: { $ref: "#" }, then: { $ref: "#" }, else: { $ref: "#" }, allOf: { $ref: "#/definitions/schemaArray" }, anyOf: { $ref: "#/definitions/schemaArray" }, oneOf: { $ref: "#/definitions/schemaArray" }, not: { $ref: "#" } }, default: true };
});
var px = k((Rt, dx) => {
  Object.defineProperty(Rt, "__esModule", { value: true });
  Rt.MissingRefError = Rt.ValidationError = Rt.CodeGen = Rt.Name = Rt.nil = Rt.stringify = Rt.str = Rt._ = Rt.KeywordCxt = Rt.Ajv = void 0;
  var Q7 = oM(), eQ = VD(), tQ = QD(), tN = eN(), rQ = ["/properties"], eg = "http://json-schema.org/draft-07/schema";
  class Jl extends Q7.default {
    _addVocabularies() {
      if (super._addVocabularies(), eQ.default.forEach((e) => this.addVocabulary(e)), this.opts.discriminator) this.addKeyword(tQ.default);
    }
    _addDefaultMetaSchema() {
      if (super._addDefaultMetaSchema(), !this.opts.meta) return;
      let e = this.opts.$data ? this.$dataMetaSchema(tN, rQ) : tN;
      this.addMetaSchema(e, eg, false), this.refs["http://json-schema.org/schema"] = eg;
    }
    defaultMeta() {
      return this.opts.defaultMeta = super.defaultMeta() || (this.getSchema(eg) ? eg : void 0);
    }
  }
  Rt.Ajv = Jl;
  dx.exports = Rt = Jl;
  dx.exports.Ajv = Jl;
  Object.defineProperty(Rt, "__esModule", { value: true });
  Rt.default = Jl;
  var nQ = jl();
  Object.defineProperty(Rt, "KeywordCxt", { enumerable: true, get: function() {
    return nQ.KeywordCxt;
  } });
  var Ts = re();
  Object.defineProperty(Rt, "_", { enumerable: true, get: function() {
    return Ts._;
  } });
  Object.defineProperty(Rt, "str", { enumerable: true, get: function() {
    return Ts.str;
  } });
  Object.defineProperty(Rt, "stringify", { enumerable: true, get: function() {
    return Ts.stringify;
  } });
  Object.defineProperty(Rt, "nil", { enumerable: true, get: function() {
    return Ts.nil;
  } });
  Object.defineProperty(Rt, "Name", { enumerable: true, get: function() {
    return Ts.Name;
  } });
  Object.defineProperty(Rt, "CodeGen", { enumerable: true, get: function() {
    return Ts.CodeGen;
  } });
  var oQ = Mm();
  Object.defineProperty(Rt, "ValidationError", { enumerable: true, get: function() {
    return oQ.default;
  } });
  var iQ = Ul();
  Object.defineProperty(Rt, "MissingRefError", { enumerable: true, get: function() {
    return iQ.default;
  } });
});
var dN = k((lN) => {
  Object.defineProperty(lN, "__esModule", { value: true });
  lN.formatNames = lN.fastFormats = lN.fullFormats = void 0;
  function Ir(e, t) {
    return { validate: e, compare: t };
  }
  lN.fullFormats = { date: Ir(iN, hx), time: Ir(mx(true), yx), "date-time": Ir(rN(true), aN), "iso-time": Ir(mx(), sN), "iso-date-time": Ir(rN(), cN), duration: /^P(?!$)((\d+Y)?(\d+M)?(\d+D)?(T(?=\d)(\d+H)?(\d+M)?(\d+S)?)?|(\d+W)?)$/, uri: fQ, "uri-reference": /^(?:[a-z][a-z0-9+\-.]*:)?(?:\/?\/(?:(?:[a-z0-9\-._~!$&'()*+,;=:]|%[0-9a-f]{2})*@)?(?:\[(?:(?:(?:(?:[0-9a-f]{1,4}:){6}|::(?:[0-9a-f]{1,4}:){5}|(?:[0-9a-f]{1,4})?::(?:[0-9a-f]{1,4}:){4}|(?:(?:[0-9a-f]{1,4}:){0,1}[0-9a-f]{1,4})?::(?:[0-9a-f]{1,4}:){3}|(?:(?:[0-9a-f]{1,4}:){0,2}[0-9a-f]{1,4})?::(?:[0-9a-f]{1,4}:){2}|(?:(?:[0-9a-f]{1,4}:){0,3}[0-9a-f]{1,4})?::[0-9a-f]{1,4}:|(?:(?:[0-9a-f]{1,4}:){0,4}[0-9a-f]{1,4})?::)(?:[0-9a-f]{1,4}:[0-9a-f]{1,4}|(?:(?:25[0-5]|2[0-4]\d|[01]?\d\d?)\.){3}(?:25[0-5]|2[0-4]\d|[01]?\d\d?))|(?:(?:[0-9a-f]{1,4}:){0,5}[0-9a-f]{1,4})?::[0-9a-f]{1,4}|(?:(?:[0-9a-f]{1,4}:){0,6}[0-9a-f]{1,4})?::)|[Vv][0-9a-f]+\.[a-z0-9\-._~!$&'()*+,;=:]+)\]|(?:(?:25[0-5]|2[0-4]\d|[01]?\d\d?)\.){3}(?:25[0-5]|2[0-4]\d|[01]?\d\d?)|(?:[a-z0-9\-._~!$&'"()*+,;=]|%[0-9a-f]{2})*)(?::\d*)?(?:\/(?:[a-z0-9\-._~!$&'"()*+,;=:@]|%[0-9a-f]{2})*)*|\/(?:(?:[a-z0-9\-._~!$&'"()*+,;=:@]|%[0-9a-f]{2})+(?:\/(?:[a-z0-9\-._~!$&'"()*+,;=:@]|%[0-9a-f]{2})*)*)?|(?:[a-z0-9\-._~!$&'"()*+,;=:@]|%[0-9a-f]{2})+(?:\/(?:[a-z0-9\-._~!$&'"()*+,;=:@]|%[0-9a-f]{2})*)*)?(?:\?(?:[a-z0-9\-._~!$&'"()*+,;=:@/?]|%[0-9a-f]{2})*)?(?:#(?:[a-z0-9\-._~!$&'"()*+,;=:@/?]|%[0-9a-f]{2})*)?$/i, "uri-template": /^(?:(?:[^\x00-\x20"'<>%\\^`{|}]|%[0-9a-f]{2})|\{[+#./;?&=,!@|]?(?:[a-z0-9_]|%[0-9a-f]{2})+(?::[1-9][0-9]{0,3}|\*)?(?:,(?:[a-z0-9_]|%[0-9a-f]{2})+(?::[1-9][0-9]{0,3}|\*)?)*\})*$/i, url: /^(?:https?|ftp):\/\/(?:\S+(?::\S*)?@)?(?:(?!(?:10|127)(?:\.\d{1,3}){3})(?!(?:169\.254|192\.168)(?:\.\d{1,3}){2})(?!172\.(?:1[6-9]|2\d|3[0-1])(?:\.\d{1,3}){2})(?:[1-9]\d?|1\d\d|2[01]\d|22[0-3])(?:\.(?:1?\d{1,2}|2[0-4]\d|25[0-5])){2}(?:\.(?:[1-9]\d?|1\d\d|2[0-4]\d|25[0-4]))|(?:(?:[a-z0-9\u{00a1}-\u{ffff}]+-)*[a-z0-9\u{00a1}-\u{ffff}]+)(?:\.(?:[a-z0-9\u{00a1}-\u{ffff}]+-)*[a-z0-9\u{00a1}-\u{ffff}]+)*(?:\.(?:[a-z\u{00a1}-\u{ffff}]{2,})))(?::\d{2,5})?(?:\/[^\s]*)?$/iu, email: /^[a-z0-9!#$%&'*+/=?^_`{|}~-]+(?:\.[a-z0-9!#$%&'*+/=?^_`{|}~-]+)*@(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?\.)+[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/i, hostname: /^(?=.{1,253}\.?$)[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[-0-9a-z]{0,61}[0-9a-z])?)*\.?$/i, ipv4: /^(?:(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)$/, ipv6: /^((([0-9a-f]{1,4}:){7}([0-9a-f]{1,4}|:))|(([0-9a-f]{1,4}:){6}(:[0-9a-f]{1,4}|((25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3})|:))|(([0-9a-f]{1,4}:){5}(((:[0-9a-f]{1,4}){1,2})|:((25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3})|:))|(([0-9a-f]{1,4}:){4}(((:[0-9a-f]{1,4}){1,3})|((:[0-9a-f]{1,4})?:((25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}))|:))|(([0-9a-f]{1,4}:){3}(((:[0-9a-f]{1,4}){1,4})|((:[0-9a-f]{1,4}){0,2}:((25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}))|:))|(([0-9a-f]{1,4}:){2}(((:[0-9a-f]{1,4}){1,5})|((:[0-9a-f]{1,4}){0,3}:((25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}))|:))|(([0-9a-f]{1,4}:){1}(((:[0-9a-f]{1,4}){1,6})|((:[0-9a-f]{1,4}){0,4}:((25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}))|:))|(:(((:[0-9a-f]{1,4}){1,7})|((:[0-9a-f]{1,4}){0,5}:((25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}))|:)))$/i, regex: vQ, uuid: /^(?:urn:uuid:)?[0-9a-f]{8}-(?:[0-9a-f]{4}-){3}[0-9a-f]{12}$/i, "json-pointer": /^(?:\/(?:[^~/]|~0|~1)*)*$/, "json-pointer-uri-fragment": /^#(?:\/(?:[a-z0-9_\-.!$&'()*+,;:=@]|%[0-9a-f]{2}|~0|~1)*)*$/i, "relative-json-pointer": /^(?:0|[1-9][0-9]*)(?:#|(?:\/(?:[^~/]|~0|~1)*)*)$/, byte: mQ, int32: { type: "number", validate: yQ }, int64: { type: "number", validate: bQ }, float: { type: "number", validate: oN }, double: { type: "number", validate: oN }, password: true, binary: true };
  lN.fastFormats = { ...lN.fullFormats, date: Ir(/^\d\d\d\d-[0-1]\d-[0-3]\d$/, hx), time: Ir(/^(?:[0-2]\d:[0-5]\d:[0-5]\d|23:59:60)(?:\.\d+)?(?:z|[+-]\d\d(?::?\d\d)?)$/i, yx), "date-time": Ir(/^\d\d\d\d-[0-1]\d-[0-3]\dt(?:[0-2]\d:[0-5]\d:[0-5]\d|23:59:60)(?:\.\d+)?(?:z|[+-]\d\d(?::?\d\d)?)$/i, aN), "iso-time": Ir(/^(?:[0-2]\d:[0-5]\d:[0-5]\d|23:59:60)(?:\.\d+)?(?:z|[+-]\d\d(?::?\d\d)?)?$/i, sN), "iso-date-time": Ir(/^\d\d\d\d-[0-1]\d-[0-3]\d[t\s](?:[0-2]\d:[0-5]\d:[0-5]\d|23:59:60)(?:\.\d+)?(?:z|[+-]\d\d(?::?\d\d)?)?$/i, cN), uri: /^(?:[a-z][a-z0-9+\-.]*:)(?:\/?\/)?[^\s]*$/i, "uri-reference": /^(?:(?:[a-z][a-z0-9+\-.]*:)?\/?\/)?(?:[^\\\s#][^\s#]*)?(?:#[^\\\s]*)?$/i, email: /^[a-z0-9.!#$%&'*+/=?^_`{|}~-]+@[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*$/i };
  lN.formatNames = Object.keys(lN.fullFormats);
  function cQ(e) {
    return e % 4 === 0 && (e % 100 !== 0 || e % 400 === 0);
  }
  var lQ = /^(\d\d\d\d)-(\d\d)-(\d\d)$/, uQ = [0, 31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31];
  function iN(e) {
    let t = lQ.exec(e);
    if (!t) return false;
    let r = +t[1], o = +t[2], n = +t[3];
    return o >= 1 && o <= 12 && n >= 1 && n <= (o === 2 && cQ(r) ? 29 : uQ[o]);
  }
  function hx(e, t) {
    if (!(e && t)) return;
    if (e > t) return 1;
    if (e < t) return -1;
    return 0;
  }
  var fx = /^(\d\d):(\d\d):(\d\d(?:\.\d+)?)(z|([+-])(\d\d)(?::?(\d\d))?)?$/i;
  function mx(e) {
    return function(r) {
      let o = fx.exec(r);
      if (!o) return false;
      let n = +o[1], i = +o[2], s = +o[3], a = o[4], c = o[5] === "-" ? -1 : 1, u = +(o[6] || 0), d = +(o[7] || 0);
      if (u > 23 || d > 59 || e && !a) return false;
      if (n <= 23 && i <= 59 && s < 60) return true;
      let p = i - d * c, f = n - u * c - (p < 0 ? 1 : 0);
      return (f === 23 || f === -1) && (p === 59 || p === -1) && s < 61;
    };
  }
  function yx(e, t) {
    if (!(e && t)) return;
    let r = (/* @__PURE__ */ new Date("2020-01-01T" + e)).valueOf(), o = (/* @__PURE__ */ new Date("2020-01-01T" + t)).valueOf();
    if (!(r && o)) return;
    return r - o;
  }
  function sN(e, t) {
    if (!(e && t)) return;
    let r = fx.exec(e), o = fx.exec(t);
    if (!(r && o)) return;
    if (e = r[1] + r[2] + r[3], t = o[1] + o[2] + o[3], e > t) return 1;
    if (e < t) return -1;
    return 0;
  }
  var gx = /t|\s/i;
  function rN(e) {
    let t = mx(e);
    return function(o) {
      let n = o.split(gx);
      return n.length === 2 && iN(n[0]) && t(n[1]);
    };
  }
  function aN(e, t) {
    if (!(e && t)) return;
    let r = new Date(e).valueOf(), o = new Date(t).valueOf();
    if (!(r && o)) return;
    return r - o;
  }
  function cN(e, t) {
    if (!(e && t)) return;
    let [r, o] = e.split(gx), [n, i] = t.split(gx), s = hx(r, n);
    if (s === void 0) return;
    return s || yx(o, i);
  }
  var dQ = /\/|:/, pQ = /^(?:[a-z][a-z0-9+\-.]*:)(?:\/?\/(?:(?:[a-z0-9\-._~!$&'()*+,;=:]|%[0-9a-f]{2})*@)?(?:\[(?:(?:(?:(?:[0-9a-f]{1,4}:){6}|::(?:[0-9a-f]{1,4}:){5}|(?:[0-9a-f]{1,4})?::(?:[0-9a-f]{1,4}:){4}|(?:(?:[0-9a-f]{1,4}:){0,1}[0-9a-f]{1,4})?::(?:[0-9a-f]{1,4}:){3}|(?:(?:[0-9a-f]{1,4}:){0,2}[0-9a-f]{1,4})?::(?:[0-9a-f]{1,4}:){2}|(?:(?:[0-9a-f]{1,4}:){0,3}[0-9a-f]{1,4})?::[0-9a-f]{1,4}:|(?:(?:[0-9a-f]{1,4}:){0,4}[0-9a-f]{1,4})?::)(?:[0-9a-f]{1,4}:[0-9a-f]{1,4}|(?:(?:25[0-5]|2[0-4]\d|[01]?\d\d?)\.){3}(?:25[0-5]|2[0-4]\d|[01]?\d\d?))|(?:(?:[0-9a-f]{1,4}:){0,5}[0-9a-f]{1,4})?::[0-9a-f]{1,4}|(?:(?:[0-9a-f]{1,4}:){0,6}[0-9a-f]{1,4})?::)|[Vv][0-9a-f]+\.[a-z0-9\-._~!$&'()*+,;=:]+)\]|(?:(?:25[0-5]|2[0-4]\d|[01]?\d\d?)\.){3}(?:25[0-5]|2[0-4]\d|[01]?\d\d?)|(?:[a-z0-9\-._~!$&'()*+,;=]|%[0-9a-f]{2})*)(?::\d*)?(?:\/(?:[a-z0-9\-._~!$&'()*+,;=:@]|%[0-9a-f]{2})*)*|\/(?:(?:[a-z0-9\-._~!$&'()*+,;=:@]|%[0-9a-f]{2})+(?:\/(?:[a-z0-9\-._~!$&'()*+,;=:@]|%[0-9a-f]{2})*)*)?|(?:[a-z0-9\-._~!$&'()*+,;=:@]|%[0-9a-f]{2})+(?:\/(?:[a-z0-9\-._~!$&'()*+,;=:@]|%[0-9a-f]{2})*)*)(?:\?(?:[a-z0-9\-._~!$&'()*+,;=:@/?]|%[0-9a-f]{2})*)?(?:#(?:[a-z0-9\-._~!$&'()*+,;=:@/?]|%[0-9a-f]{2})*)?$/i;
  function fQ(e) {
    return dQ.test(e) && pQ.test(e);
  }
  var nN = /^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/gm;
  function mQ(e) {
    return nN.lastIndex = 0, nN.test(e);
  }
  var gQ = -2147483648, hQ = 2147483647;
  function yQ(e) {
    return Number.isInteger(e) && e <= hQ && e >= gQ;
  }
  function bQ(e) {
    return Number.isInteger(e);
  }
  function oN() {
    return true;
  }
  var _Q = /[^\\]\\Z/;
  function vQ(e) {
    if (_Q.test(e)) return false;
    try {
      return new RegExp(e), true;
    } catch (t) {
      return false;
    }
  }
});
var fN = k((pN) => {
  Object.defineProperty(pN, "__esModule", { value: true });
  pN.formatLimitDefinition = void 0;
  var xQ = px(), br = re(), qn = br.operators, tg = { formatMaximum: { okStr: "<=", ok: qn.LTE, fail: qn.GT }, formatMinimum: { okStr: ">=", ok: qn.GTE, fail: qn.LT }, formatExclusiveMaximum: { okStr: "<", ok: qn.LT, fail: qn.GTE }, formatExclusiveMinimum: { okStr: ">", ok: qn.GT, fail: qn.LTE } }, wQ = { message: ({ keyword: e, schemaCode: t }) => br.str`should be ${tg[e].okStr} ${t}`, params: ({ keyword: e, schemaCode: t }) => br._`{comparison: ${tg[e].okStr}, limit: ${t}}` };
  pN.formatLimitDefinition = { keyword: Object.keys(tg), type: "string", schemaType: "string", $data: true, error: wQ, code(e) {
    let { gen: t, data: r, schemaCode: o, keyword: n, it: i } = e, { opts: s, self: a } = i;
    if (!s.validateFormats) return;
    let c = new xQ.KeywordCxt(i, a.RULES.all.format.definition, "format");
    if (c.$data) u();
    else d();
    function u() {
      let f = t.scopeValue("formats", { ref: a.formats, code: s.code.formats }), m = t.const("fmt", br._`${f}[${c.schemaCode}]`);
      e.fail$data((0, br.or)(br._`typeof ${m} != "object"`, br._`${m} instanceof RegExp`, br._`typeof ${m}.compare != "function"`, p(m)));
    }
    function d() {
      let f = c.schema, m = a.formats[f];
      if (!m || m === true) return;
      if (typeof m != "object" || m instanceof RegExp || typeof m.compare != "function") throw Error(`"${n}": format "${f}" does not define "compare" function`);
      let g = t.scopeValue("formats", { key: f, ref: m, code: s.code.formats ? br._`${s.code.formats}${(0, br.getProperty)(f)}` : void 0 });
      e.fail$data(p(g));
    }
    function p(f) {
      return br._`${f}.compare(${r}, ${o}) ${tg[n].fail} 0`;
    }
  }, dependencies: ["format"] };
  var kQ = (e) => (e.addKeyword(pN.formatLimitDefinition), e);
  pN.default = kQ;
});
var yN = k((Xl, hN) => {
  Object.defineProperty(Xl, "__esModule", { value: true });
  var Ps = dN(), TQ = fN(), vx = re(), mN = new vx.Name("fullFormats"), PQ = new vx.Name("fastFormats"), Sx = (e, t = { keywords: true }) => {
    if (Array.isArray(t)) return gN(e, t, Ps.fullFormats, mN), e;
    let [r, o] = t.mode === "fast" ? [Ps.fastFormats, PQ] : [Ps.fullFormats, mN], n = t.formats || Ps.formatNames;
    if (gN(e, n, r, o), t.keywords) (0, TQ.default)(e);
    return e;
  };
  Sx.get = (e, t = "full") => {
    let o = (t === "fast" ? Ps.fastFormats : Ps.fullFormats)[e];
    if (!o) throw Error(`Unknown format "${e}"`);
    return o;
  };
  function gN(e, t, r, o) {
    var n, i;
    (n = (i = e.opts.code).formats) !== null && n !== void 0 || (i.formats = vx._`require("ajv-formats/dist/formats").${o}`);
    for (let s of t) e.addFormat(s, r[s]);
  }
  hN.exports = Xl = Sx;
  Object.defineProperty(Xl, "__esModule", { value: true });
  Xl.default = Sx;
});
var Dz = 50;
function qs(e = Dz) {
  let t = new AbortController();
  return Mz(e, t.signal), t;
}
function Or(e) {
  return process.platform === "darwin" ? e.normalize("NFC") : e;
}
function Ig(e) {
  return /^[\\/]{2}/.test(e);
}
function Rg(e) {
  return /^[\\/]{2}wsl(\$|\.localhost)[\\/]/i.test(e);
}
function Sw(e) {
  if (e.startsWith("\\\\?\\UNC\\")) return "\\\\" + e.slice(8);
  if (e.startsWith("\\\\?\\") && e.length >= 7 && e[5] === ":") return e.slice(4);
  return e;
}
function xw(e) {
  if (/^\\\\\?\\volume\{/i.test(e)) return _w(e);
  let t = Sw(e);
  if (t !== e && _w(t)) return true;
  return Ig(t) && !Rg(t);
}
function _w(e) {
  return /(^|[\\/])\.{1,2}([\\/]|$)/.test(e) || e.includes("/");
}
function yu(e, t, r) {
  return new Promise((o, n) => {
    if (t?.aborted) {
      if (r?.throwOnAbort || r?.abortError) n(r.abortError?.() ?? Error("aborted"));
      else o();
      return;
    }
    let i = setTimeout((a, c, u) => {
      a?.removeEventListener("abort", c), u();
    }, e, t, s, o);
    function s() {
      if (clearTimeout(i), r?.throwOnAbort || r?.abortError) n(r.abortError?.() ?? Error("aborted"));
      else o();
    }
    if (t?.addEventListener("abort", s, { once: true }), r?.unref) i.unref();
  });
}
function jz(e, t) {
  e(Error(t));
}
function Ar(e, t, r) {
  let o, n = new Promise((i, s) => {
    o = setTimeout(jz, t, s, r);
  });
  return Promise.race([e, n]).finally(() => {
    if (o !== void 0) clearTimeout(o);
  });
}
var Xo = ["PreToolUse", "PostToolUse", "PostToolUseFailure", "PostToolBatch", "Notification", "UserPromptSubmit", "UserPromptExpansion", "SessionStart", "SessionEnd", "Stop", "StopFailure", "SubagentStart", "SubagentStop", "PreCompact", "PostCompact", "PermissionRequest", "PermissionDenied", "Setup", "TeammateIdle", "TaskCreated", "TaskCompleted", "Elicitation", "ElicitationResult", "ConfigChange", "WorktreeCreate", "WorktreeRemove", "InstructionsLoaded", "CwdChanged", "FileChanged", "MessageDisplay"];
var ot = class extends Error {
};
function _u() {
  return process.versions.bun !== void 0;
}
function Ee(e) {
  if (!e) return false;
  if (typeof e === "boolean") return e;
  let t = String(e).toLowerCase().trim();
  return ["1", "true", "yes", "on"].includes(t);
}
function un() {
  let e = /* @__PURE__ */ new Set();
  return { subscribe(t) {
    return e.add(t), () => {
      e.delete(t);
    };
  }, emit(...t) {
    let r;
    for (let o of e) try {
      o(...t);
    } catch (n) {
      (r ??= []).push(n);
    }
    if (r) throw r.length === 1 ? r[0] : AggregateError(r, "Signal listener(s) threw");
  }, clear() {
    e.clear();
  } };
}
var Wz = typeof global == "object" && global && global.Object === Object && global;
var vu = Wz;
var Kz = typeof self == "object" && self && self.Object === Object && self;
var Gz = vu || Kz || Function("return this")();
var Bt = Gz;
var Jz = Bt.Symbol;
var Ht = Jz;
var Ew = Object.prototype;
var Xz = Ew.hasOwnProperty;
var Yz = Ew.toString;
var Vs = Ht ? Ht.toStringTag : void 0;
function Qz(e) {
  var t = Xz.call(e, Vs), r = e[Vs];
  try {
    e[Vs] = void 0;
    var o = true;
  } catch (i) {
  }
  var n = Yz.call(e);
  if (o) if (t) e[Vs] = r;
  else delete e[Vs];
  return n;
}
var Tw = Qz;
var eL = Object.prototype;
var tL = eL.toString;
function rL(e) {
  return tL.call(e);
}
var Pw = rL;
var nL = "[object Null]";
var oL = "[object Undefined]";
var Iw = Ht ? Ht.toStringTag : void 0;
function iL(e) {
  if (e == null) return e === void 0 ? oL : nL;
  return Iw && Iw in Object(e) ? Tw(e) : Pw(e);
}
var wr = iL;
function sL(e) {
  var t = typeof e;
  return e != null && (t == "object" || t == "function");
}
var Qe = sL;
var aL = "[object AsyncFunction]";
var cL = "[object Function]";
var lL = "[object GeneratorFunction]";
var uL = "[object Proxy]";
function dL(e) {
  if (!Qe(e)) return false;
  var t = wr(e);
  return t == cL || t == lL || t == aL || t == uL;
}
var Yo = dL;
var pL = Bt["__core-js_shared__"];
var Su = pL;
var Rw = function() {
  var e = /[^.]+$/.exec(Su && Su.keys && Su.keys.IE_PROTO || "");
  return e ? "Symbol(src)_1." + e : "";
}();
function fL(e) {
  return !!Rw && Rw in e;
}
var $w = fL;
var mL = Function.prototype;
var gL = mL.toString;
function hL(e) {
  if (e != null) {
    try {
      return gL.call(e);
    } catch (t) {
    }
    try {
      return e + "";
    } catch (t) {
    }
  }
  return "";
}
var Ow = hL;
var yL = /[\\^$.*+?()[\]{}|]/g;
var bL = /^\[object .+?Constructor\]$/;
var _L = Function.prototype;
var vL = Object.prototype;
var SL = _L.toString;
var xL = vL.hasOwnProperty;
var wL = RegExp("^" + SL.call(xL).replace(yL, "\\$&").replace(/hasOwnProperty|(function).*?(?=\\\()| for .+?(?=\\\])/g, "$1.*?") + "$");
function kL(e) {
  if (!Qe(e) || $w(e)) return false;
  var t = Yo(e) ? wL : bL;
  return t.test(Ow(e));
}
var Aw = kL;
function EL(e, t) {
  return e == null ? void 0 : e[t];
}
var Cw = EL;
function TL(e, t) {
  var r = Cw(e, t);
  return Aw(r) ? r : void 0;
}
var Qo = TL;
var PL = Qo(Object, "create");
var Cr = PL;
function IL() {
  this.__data__ = Cr ? Cr(null) : {}, this.size = 0;
}
var Mw = IL;
function RL(e) {
  var t = this.has(e) && delete this.__data__[e];
  return this.size -= t ? 1 : 0, t;
}
var Dw = RL;
var $L = "__lodash_hash_undefined__";
var OL = Object.prototype;
var AL = OL.hasOwnProperty;
function CL(e) {
  var t = this.__data__;
  if (Cr) {
    var r = t[e];
    return r === $L ? void 0 : r;
  }
  return AL.call(t, e) ? t[e] : void 0;
}
var Nw = CL;
var ML = Object.prototype;
var DL = ML.hasOwnProperty;
function NL(e) {
  var t = this.__data__;
  return Cr ? t[e] !== void 0 : DL.call(t, e);
}
var jw = NL;
var jL = "__lodash_hash_undefined__";
function UL(e, t) {
  var r = this.__data__;
  return this.size += this.has(e) ? 0 : 1, r[e] = Cr && t === void 0 ? jL : t, this;
}
var Uw = UL;
function ei(e) {
  var t = -1, r = e == null ? 0 : e.length;
  this.clear();
  while (++t < r) {
    var o = e[t];
    this.set(o[0], o[1]);
  }
}
ei.prototype.clear = Mw;
ei.prototype.delete = Dw;
ei.prototype.get = Nw;
ei.prototype.has = jw;
ei.prototype.set = Uw;
var Og = ei;
function zL() {
  this.__data__ = [], this.size = 0;
}
var zw = zL;
function LL(e, t) {
  return e === t || e !== e && t !== t;
}
var dn = LL;
function FL(e, t) {
  var r = e.length;
  while (r--) if (dn(e[r][0], t)) return r;
  return -1;
}
var pn = FL;
var BL = Array.prototype;
var HL = BL.splice;
function qL(e) {
  var t = this.__data__, r = pn(t, e);
  if (r < 0) return false;
  var o = t.length - 1;
  if (r == o) t.pop();
  else HL.call(t, r, 1);
  return --this.size, true;
}
var Lw = qL;
function ZL(e) {
  var t = this.__data__, r = pn(t, e);
  return r < 0 ? void 0 : t[r][1];
}
var Fw = ZL;
function VL(e) {
  return pn(this.__data__, e) > -1;
}
var Bw = VL;
function WL(e, t) {
  var r = this.__data__, o = pn(r, e);
  if (o < 0) ++this.size, r.push([e, t]);
  else r[o][1] = t;
  return this;
}
var Hw = WL;
function ti(e) {
  var t = -1, r = e == null ? 0 : e.length;
  this.clear();
  while (++t < r) {
    var o = e[t];
    this.set(o[0], o[1]);
  }
}
ti.prototype.clear = zw;
ti.prototype.delete = Lw;
ti.prototype.get = Fw;
ti.prototype.has = Bw;
ti.prototype.set = Hw;
var fn = ti;
var KL = Qo(Bt, "Map");
var xu = KL;
function GL() {
  this.size = 0, this.__data__ = { hash: new Og(), map: new (xu || fn)(), string: new Og() };
}
var qw = GL;
function JL(e) {
  var t = typeof e;
  return t == "string" || t == "number" || t == "symbol" || t == "boolean" ? e !== "__proto__" : e === null;
}
var Zw = JL;
function XL(e, t) {
  var r = e.__data__;
  return Zw(t) ? r[typeof t == "string" ? "string" : "hash"] : r.map;
}
var mn = XL;
function YL(e) {
  var t = mn(this, e).delete(e);
  return this.size -= t ? 1 : 0, t;
}
var Vw = YL;
function QL(e) {
  return mn(this, e).get(e);
}
var Ww = QL;
function e1(e) {
  return mn(this, e).has(e);
}
var Kw = e1;
function t1(e, t) {
  var r = mn(this, e), o = r.size;
  return r.set(e, t), this.size += r.size == o ? 0 : 1, this;
}
var Gw = t1;
function ri(e) {
  var t = -1, r = e == null ? 0 : e.length;
  this.clear();
  while (++t < r) {
    var o = e[t];
    this.set(o[0], o[1]);
  }
}
ri.prototype.clear = qw;
ri.prototype.delete = Vw;
ri.prototype.get = Ww;
ri.prototype.has = Kw;
ri.prototype.set = Gw;
var Ws = ri;
var r1 = "Expected a function";
function Ag(e, t) {
  if (typeof e != "function" || t != null && typeof t != "function") throw TypeError(r1);
  var r = function() {
    var o = arguments, n = t ? t.apply(this, o) : o[0], i = r.cache;
    if (i.has(n)) return i.get(n);
    var s = e.apply(this, o);
    return r.cache = i.set(n, s) || i, s;
  };
  return r.cache = new (Ag.Cache || Ws)(), r;
}
Ag.Cache = Ws;
var Ce = Ag;
var qt = Ce(() => (process.env.CLAUDE_CONFIG_DIR ?? o1(n1(), ".claude")).normalize("NFC"), () => process.env.CLAUDE_CONFIG_DIR);
var Iie = Ce(() => Ee(process.env.CLAUDE_CODE_SUPERVISED));
function N(e, t, r, o, n) {
  if (o === "m") throw TypeError("Private method is not writable");
  if (o === "a" && !n) throw TypeError("Private accessor was defined without a setter");
  if (typeof t === "function" ? e !== t || !n : !t.has(e)) throw TypeError("Cannot write private member to an object whose class did not declare it");
  return o === "a" ? n.call(e, r) : n ? n.value = r : t.set(e, r), r;
}
function _(e, t, r, o) {
  if (r === "a" && !o) throw TypeError("Private accessor was defined without a getter");
  if (typeof t === "function" ? e !== t || !o : !t.has(e)) throw TypeError("Cannot read private member from an object whose class did not declare it");
  return r === "m" ? o : r === "a" ? o.call(e) : o ? o.value : t.get(e);
}
var Cg = function() {
  let { crypto: e } = globalThis;
  if (e?.randomUUID) return Cg = e.randomUUID.bind(e), e.randomUUID();
  let t = new Uint8Array(1), r = e ? () => e.getRandomValues(t)[0] : () => Math.random() * 255 & 255;
  return "10000000-1000-4000-8000-100000000000".replace(/[018]/g, (o) => (+o ^ r() & 15 >> +o / 4).toString(16));
};
function Mr(e) {
  return typeof e === "object" && e !== null && ("name" in e && e.name === "AbortError" || "message" in e && String(e.message).includes("FetchRequestCanceledException"));
}
var Ks = (e) => {
  if (e instanceof Error) return e;
  if (typeof e === "object" && e !== null) {
    try {
      if (Object.prototype.toString.call(e) === "[object Error]") {
        let t = Error(e.message, e.cause ? { cause: e.cause } : {});
        if (e.stack) t.stack = e.stack;
        if (e.cause && !t.cause) t.cause = e.cause;
        if (e.name) t.name = e.name;
        return t;
      }
    } catch {
    }
    try {
      return Error(JSON.stringify(e));
    } catch {
    }
  }
  return Error(e);
};
var z = class extends Error {
};
var We = class _We extends z {
  constructor(e, t, r, o, n) {
    super(`${_We.makeMessage(e, t, r)}`);
    this.status = e, this.headers = o, this.requestID = o?.get("request-id"), this.error = t, this.type = n ?? null;
  }
  static makeMessage(e, t, r) {
    let o = t?.message ? typeof t.message === "string" ? t.message : JSON.stringify(t.message) : t ? JSON.stringify(t) : r;
    if (e && o) return `${e} ${o}`;
    if (e) return `${e} status code (no body)`;
    if (o) return o;
    return "(no status code or body)";
  }
  static generate(e, t, r, o) {
    if (!e || !o) return new to({ message: r, cause: Ks(t) });
    let n = t, i = n?.error?.type;
    if (e === 400) return new Js(e, n, r, o, i);
    if (e === 401) return new Xs(e, n, r, o, i);
    if (e === 403) return new Ys(e, n, r, o, i);
    if (e === 404) return new Qs(e, n, r, o, i);
    if (e === 409) return new ea(e, n, r, o, i);
    if (e === 422) return new ta(e, n, r, o, i);
    if (e === 429) return new ra(e, n, r, o, i);
    if (e >= 500) return new na(e, n, r, o, i);
    return new _We(e, n, r, o, i);
  }
};
var et = class extends We {
  constructor({ message: e } = {}) {
    super(void 0, void 0, e || "Request was aborted.", void 0);
  }
};
var to = class extends We {
  constructor({ message: e, cause: t }) {
    super(void 0, void 0, e || "Connection error.", void 0);
    if (t) this.cause = t;
  }
};
var Gs = class extends to {
  constructor({ message: e } = {}) {
    super({ message: e ?? "Request timed out." });
  }
};
var Js = class extends We {
};
var Xs = class extends We {
};
var Ys = class extends We {
};
var Qs = class extends We {
};
var ea = class extends We {
};
var ta = class extends We {
};
var ra = class extends We {
};
var na = class extends We {
};
var s1 = /^[a-z][a-z0-9+.-]*:/i;
var Jw = (e) => s1.test(e);
var Mg = (e) => (Mg = Array.isArray, Mg(e));
var Dg = Mg;
function wu(e) {
  if (typeof e !== "object") return {};
  return e ?? {};
}
function Ng(e) {
  if (!e) return true;
  for (let t in e) return false;
  return true;
}
function Xw(e, t) {
  return Object.prototype.hasOwnProperty.call(e, t);
}
var Yw = (e, t) => {
  if (typeof t !== "number" || !Number.isInteger(t)) throw new z(`${e} must be an integer`);
  if (t < 0) throw new z(`${e} must be a positive integer`);
  return t;
};
var ku = (e) => {
  try {
    return JSON.parse(e);
  } catch (t) {
    return;
  }
};
var Qw = (e) => new Promise((t) => setTimeout(t, e));
var Zt = "0.94.0";
var nk = () => typeof window < "u" && typeof window.document < "u" && typeof navigator < "u";
function a1() {
  if (typeof Deno < "u" && Deno.build != null) return "deno";
  if (typeof EdgeRuntime < "u") return "edge";
  if (Object.prototype.toString.call(typeof globalThis.process < "u" ? globalThis.process : 0) === "[object process]") return "node";
  return "unknown";
}
var c1 = () => {
  let e = a1();
  if (e === "deno") return { "X-Stainless-Lang": "js", "X-Stainless-Package-Version": Zt, "X-Stainless-OS": tk(Deno.build.os), "X-Stainless-Arch": ek(Deno.build.arch), "X-Stainless-Runtime": "deno", "X-Stainless-Runtime-Version": typeof Deno.version === "string" ? Deno.version : Deno.version?.deno ?? "unknown" };
  if (typeof EdgeRuntime < "u") return { "X-Stainless-Lang": "js", "X-Stainless-Package-Version": Zt, "X-Stainless-OS": "Unknown", "X-Stainless-Arch": `other:${EdgeRuntime}`, "X-Stainless-Runtime": "edge", "X-Stainless-Runtime-Version": globalThis.process.version };
  if (e === "node") return { "X-Stainless-Lang": "js", "X-Stainless-Package-Version": Zt, "X-Stainless-OS": tk(globalThis.process.platform ?? "unknown"), "X-Stainless-Arch": ek(globalThis.process.arch ?? "unknown"), "X-Stainless-Runtime": "node", "X-Stainless-Runtime-Version": globalThis.process.version ?? "unknown" };
  let t = l1();
  if (t) return { "X-Stainless-Lang": "js", "X-Stainless-Package-Version": Zt, "X-Stainless-OS": "Unknown", "X-Stainless-Arch": "unknown", "X-Stainless-Runtime": `browser:${t.browser}`, "X-Stainless-Runtime-Version": t.version };
  return { "X-Stainless-Lang": "js", "X-Stainless-Package-Version": Zt, "X-Stainless-OS": "Unknown", "X-Stainless-Arch": "unknown", "X-Stainless-Runtime": "unknown", "X-Stainless-Runtime-Version": "unknown" };
};
function l1() {
  if (typeof navigator > "u" || !navigator) return null;
  let e = [{ key: "edge", pattern: /Edge(?:\W+(\d+)\.(\d+)(?:\.(\d+))?)?/ }, { key: "ie", pattern: /MSIE(?:\W+(\d+)\.(\d+)(?:\.(\d+))?)?/ }, { key: "ie", pattern: /Trident(?:.*rv\:(\d+)\.(\d+)(?:\.(\d+))?)?/ }, { key: "chrome", pattern: /Chrome(?:\W+(\d+)\.(\d+)(?:\.(\d+))?)?/ }, { key: "firefox", pattern: /Firefox(?:\W+(\d+)\.(\d+)(?:\.(\d+))?)?/ }, { key: "safari", pattern: /(?:Version\W+(\d+)\.(\d+)(?:\.(\d+))?)?(?:\W+Mobile\S*)?\W+Safari/ }];
  for (let { key: t, pattern: r } of e) {
    let o = r.exec(navigator.userAgent);
    if (o) {
      let n = o[1] || 0, i = o[2] || 0, s = o[3] || 0;
      return { browser: t, version: `${n}.${i}.${s}` };
    }
  }
  return null;
}
var ek = (e) => {
  if (e === "x32") return "x32";
  if (e === "x86_64" || e === "x64") return "x64";
  if (e === "arm") return "arm";
  if (e === "aarch64" || e === "arm64") return "arm64";
  if (e) return `other:${e}`;
  return "unknown";
};
var tk = (e) => {
  if (e = e.toLowerCase(), e.includes("ios")) return "iOS";
  if (e === "android") return "Android";
  if (e === "darwin") return "MacOS";
  if (e === "win32") return "Windows";
  if (e === "freebsd") return "FreeBSD";
  if (e === "openbsd") return "OpenBSD";
  if (e === "linux") return "Linux";
  if (e) return `Other:${e}`;
  return "Unknown";
};
var rk;
var oa = () => rk ?? (rk = c1());
function ok() {
  if (typeof fetch < "u") return fetch;
  throw Error("`fetch` is not defined as a global; Either pass `fetch` to the client, `new Anthropic({ fetch })` or polyfill the global, `globalThis.fetch = fetch`");
}
function jg(...e) {
  let t = globalThis.ReadableStream;
  if (typeof t > "u") throw Error("`ReadableStream` is not defined as a global; You will need to polyfill it, `globalThis.ReadableStream = ReadableStream`");
  return new t(...e);
}
function Eu(e) {
  let t = Symbol.asyncIterator in e ? e[Symbol.asyncIterator]() : e[Symbol.iterator]();
  return jg({ start() {
  }, async pull(r) {
    let { done: o, value: n } = await t.next();
    if (o) r.close();
    else r.enqueue(n);
  }, async cancel() {
    await t.return?.();
  } });
}
function ia(e) {
  if (e[Symbol.asyncIterator]) return e;
  let t = e.getReader();
  return { async next() {
    try {
      let r = await t.read();
      if (r?.done) t.releaseLock();
      return r;
    } catch (r) {
      throw t.releaseLock(), r;
    }
  }, async return() {
    let r = t.cancel();
    return t.releaseLock(), await r, { done: true, value: void 0 };
  }, [Symbol.asyncIterator]() {
    return this;
  } };
}
async function ik(e) {
  if (e === null || typeof e !== "object") return;
  if (e[Symbol.asyncIterator]) {
    await e[Symbol.asyncIterator]().return?.();
    return;
  }
  let t = e.getReader(), r = t.cancel();
  t.releaseLock(), await r;
}
var sk = ({ headers: e, body: t }) => ({ bodyHeaders: { "content-type": "application/json" }, body: JSON.stringify(t) });
function ak(e) {
  return Object.entries(e).filter(([t, r]) => typeof r < "u").map(([t, r]) => {
    if (typeof r === "string" || typeof r === "number" || typeof r === "boolean") return `${encodeURIComponent(t)}=${encodeURIComponent(r)}`;
    if (r === null) return `${encodeURIComponent(t)}=`;
    throw new z(`Cannot stringify type ${typeof r}; Expected string, number, boolean, or null. If you need to pass nested query parameters, you can manually encode them, e.g. { query: { 'foo[key1]': value1, 'foo[key2]': value2 } }, and please open a GitHub issue requesting better support for your use case.`);
  }).join("&");
}
var lk = "urn:ietf:params:oauth:grant-type:jwt-bearer";
var uk = "refresh_token";
var Tu = "/v1/oauth/token";
var ro = "oauth-2025-04-20";
var dk = "oidc-federation-2026-04-01";
var pk = 120;
var ni = 30;
var fk = 5;
var ck = 1048576;
function Pu(e) {
  if (!e) return;
  let t;
  try {
    t = new URL(e);
  } catch (o) {
    throw new he(`Invalid token endpoint base URL "${e}": ${o}`);
  }
  if (t.protocol === "https:") return;
  let r = t.hostname.toLowerCase().replace(/^\[|\]$/g, "");
  if (t.protocol === "http:" && (r === "localhost" || r === "127.0.0.1" || r === "::1")) return;
  throw new he(`Refusing to send credential over non-https token endpoint "${e}"`);
}
async function Iu(e, t) {
  let r = await f1(e), o;
  try {
    o = JSON.parse(r);
  } catch {
    throw new he(`Token endpoint returned non-JSON response (status ${e.status})`, e.status, bt(r), t);
  }
  if (!o.access_token) throw new he(`Token endpoint response missing access_token: ${JSON.stringify(bt(o))}`, e.status, bt(o), t);
  if (o.token_type && o.token_type.toLowerCase() !== "bearer") throw new he(`Token endpoint response: unsupported token_type "${o.token_type}" (want Bearer)`, e.status, bt(o), t);
  return o;
}
var Ug = 2e3;
var p1 = /* @__PURE__ */ new Set(["error", "error_description", "error_uri"]);
function bt(e) {
  if (e == null) return e;
  if (typeof e === "string") {
    let t;
    try {
      t = JSON.parse(e);
    } catch {
      if (e.length <= Ug) return e;
      return e.slice(0, Ug) + `... <${e.length - Ug} more chars>`;
    }
    return JSON.stringify(bt(t));
  }
  if (typeof e === "object" && !Array.isArray(e)) {
    let t = {};
    for (let [r, o] of Object.entries(e)) if (p1.has(r)) t[r] = o;
    return t;
  }
  return null;
}
async function Ru(e, t = (r) => console.warn(`anthropic-sdk: ${r}`)) {
  if (typeof process > "u" || process.platform === "win32") return;
  let r = await import("node:fs"), o = e, n;
  try {
    o = await r.promises.realpath(e), n = await r.promises.stat(o);
  } catch {
    return;
  }
  let i = n.mode & 511;
  if (i & 18) throw new he(`Credentials file at ${o} is group/world-writable (mode 0o${i.toString(8)}); this allows other local users to plant tokens. Run \`chmod 600 ${o}\`.`);
  if (i & 36) throw new he(`Credentials file at ${o} is group/world-readable (mode 0o${i.toString(8)}); run \`chmod 600 ${o}\` before retrying.`);
  if (typeof process.getuid === "function" && n.uid !== process.getuid()) t(`credentials file at ${o} is owned by uid ${n.uid} (current process uid ${process.getuid()}); verify this is intentional.`);
}
async function $u(e, t) {
  let r = await import("node:fs"), n = (await import("node:path")).dirname(e);
  await r.promises.mkdir(n, { recursive: true, mode: 448 });
  let i = `${e}.${process.pid}.${Math.random().toString(36).slice(2)}.tmp`;
  try {
    let s = await r.promises.open(i, "w", 384);
    try {
      await s.writeFile(JSON.stringify(t, null, 2)), await s.sync();
    } finally {
      await s.close();
    }
    await r.promises.rename(i, e);
  } catch (s) {
    throw await r.promises.unlink(i).catch(() => {
    }), s;
  }
  try {
    let s = await r.promises.open(n, "r");
    try {
      await s.sync();
    } finally {
      await s.close();
    }
  } catch {
  }
}
async function f1(e) {
  if (!e.body) return "";
  let t = e.body.getReader(), r = [], o = 0;
  for (; ; ) {
    let { done: i, value: s } = await t.read();
    if (i) break;
    if (o + s.length > ck) {
      let a = ck - o;
      if (a > 0) r.push(s.subarray(0, a));
      await t.cancel();
      break;
    }
    r.push(s), o += s.length;
  }
  let n;
  if (r.length === 1) n = r[0];
  else {
    n = new Uint8Array(r.reduce((s, a) => s + a.length, 0));
    let i = 0;
    for (let s of r) n.set(s, i), i += s.length;
  }
  return new TextDecoder("utf-8").decode(n);
}
var he = class extends z {
  constructor(e, t = null, r = null, o = null) {
    super(e);
    this.statusCode = t, this.body = r, this.requestId = o;
  }
};
function or() {
  return Math.floor(Date.now() / 1e3);
}
var zg = class {
  constructor(e, t) {
    this.cached = null, this.pendingRefresh = null, this.nextForce = false, this.lastAdvisoryError = 0, this.provider = e, this.onAdvisoryRefreshError = t;
  }
  async getToken() {
    let e = this.nextForce;
    this.nextForce = false;
    let t = this.cached;
    if (e || t == null) return (await this.refresh(e)).token;
    if (t.expiresAt == null) return t.token;
    let r = t.expiresAt - or();
    if (r > pk) return t.token;
    if (r > ni) return this.backgroundRefresh(), t.token;
    return (await this.refresh()).token;
  }
  invalidate() {
    this.cached = null, this.nextForce = true;
  }
  refresh(e = false) {
    if (this.pendingRefresh && !e) return this.pendingRefresh;
    return this.doRefresh(e);
  }
  backgroundRefresh() {
    if (this.pendingRefresh) return;
    if (or() - this.lastAdvisoryError < fk) return;
    this.doRefresh().catch((e) => {
      this.lastAdvisoryError = or(), this.onAdvisoryRefreshError?.(e);
    });
  }
  doRefresh(e = false) {
    return this.pendingRefresh = this.provider(e ? { forceRefresh: true } : void 0).then((t) => (this.cached = t, this.pendingRefresh = null, t), (t) => {
      throw this.pendingRefresh = null, t;
    }), this.pendingRefresh;
  }
};
var ge = (e) => {
  if (typeof globalThis.process < "u") return globalThis.process.env?.[e]?.trim() || void 0;
  if (typeof globalThis.Deno < "u") return globalThis.Deno.env?.get?.(e)?.trim() || void 0;
  return;
};
function hk(e) {
  let t = 0;
  for (let n of e) t += n.length;
  let r = new Uint8Array(t), o = 0;
  for (let n of e) r.set(n, o), o += n.length;
  return r;
}
var mk;
function oi(e) {
  let t;
  return (mk ?? (t = new globalThis.TextEncoder(), mk = t.encode.bind(t)))(e);
}
var gk;
function Lg(e) {
  let t;
  return (gk ?? (t = new globalThis.TextDecoder(), gk = t.decode.bind(t)))(e);
}
var Au = { off: 0, error: 200, warn: 300, info: 400, debug: 500 };
var Fg = (e, t, r) => {
  if (!e) return;
  if (Xw(Au, e)) return e;
  Fe(r).warn(`${t} was set to ${JSON.stringify(e)}, expected one of ${JSON.stringify(Object.keys(Au))}`);
  return;
};
function sa() {
}
function Ou(e, t, r) {
  if (!t || Au[e] > Au[r]) return sa;
  else return t[e].bind(t);
}
var m1 = { error: sa, warn: sa, info: sa, debug: sa };
var yk = /* @__PURE__ */ new WeakMap();
function Fe(e) {
  let t = e.logger, r = e.logLevel ?? "off";
  if (!t) return m1;
  let o = yk.get(t);
  if (o && o[0] === r) return o[1];
  let n = { error: Ou("error", t, r), warn: Ou("warn", t, r), info: Ou("info", t, r), debug: Ou("debug", t, r) };
  return yk.set(t, [r, n]), n;
}
var Dr = (e) => {
  if (e.options) e.options = { ...e.options }, delete e.options.headers;
  if (e.headers) e.headers = Object.fromEntries((e.headers instanceof Headers ? [...e.headers] : Object.entries(e.headers)).map(([t, r]) => [t, t.toLowerCase() === "x-api-key" || t.toLowerCase() === "authorization" || t.toLowerCase() === "cookie" || t.toLowerCase() === "set-cookie" ? "***" : r]));
  if ("retryOfRequestLogID" in e) {
    if (e.retryOfRequestLogID) e.retryOf = e.retryOfRequestLogID;
    delete e.retryOfRequestLogID;
  }
  return e;
};
var Cu = "1.0";
var g1 = /^[A-Za-z0-9_.-]+$/;
function bk(e) {
  if (!e) throw Error("profile name is empty");
  if (e === "." || e === "..") throw Error(`profile name "${e}" is not allowed`);
  if (e.includes("/") || e.includes("\\")) throw Error(`profile name "${e}" must not contain path separators`);
  if (!g1.test(e)) throw Error(`profile name "${e}" contains disallowed characters (allowed: letters, digits, '_', '.', '-')`);
}
var _k = async (e) => {
  var t, r;
  let o = await Bg();
  if (o === null) return null;
  let n = e ?? await Sk();
  if (n === null) return null;
  bk(n);
  let i = await import("node:fs"), a = (await import("node:path")).join(o, "configs", `${n}.json`), c;
  try {
    c = await i.promises.readFile(a, "utf-8");
  } catch (p) {
    if (p?.code !== "ENOENT") throw Error(`failed to read config file ${a}: ${p}`);
    c = null;
  }
  if (c === null) {
    let p = ge("ANTHROPIC_ORGANIZATION_ID"), f = ge("ANTHROPIC_IDENTITY_TOKEN_FILE"), m = ge("ANTHROPIC_FEDERATION_RULE_ID");
    if (m && p) return { fromFile: false, config: { organization_id: p, workspace_id: ge("ANTHROPIC_WORKSPACE_ID"), base_url: ge("ANTHROPIC_BASE_URL"), authentication: { type: "oidc_federation", federation_rule_id: m, service_account_id: ge("ANTHROPIC_SERVICE_ACCOUNT_ID"), identity_token: f ? { source: "file", path: f } : void 0, scope: ge("ANTHROPIC_SCOPE") } } };
    return null;
  }
  let u;
  try {
    u = JSON.parse(c);
  } catch (p) {
    throw Error(`failed to parse config file ${a}: ${p}`);
  }
  if (!u.authentication) throw Error(`config file ${a} is missing "authentication"`);
  let d = u.authentication.type;
  if (d !== "oidc_federation" && d !== "user_oauth") throw Error(`authentication.type "${d}" is not a known authentication type`);
  if (u.organization_id ?? (u.organization_id = ge("ANTHROPIC_ORGANIZATION_ID")), u.workspace_id ?? (u.workspace_id = ge("ANTHROPIC_WORKSPACE_ID")), u.base_url ?? (u.base_url = ge("ANTHROPIC_BASE_URL")), (t = u.authentication).scope ?? (t.scope = ge("ANTHROPIC_SCOPE")), u.authentication.type === "oidc_federation") {
    if (!u.authentication.identity_token) {
      let p = ge("ANTHROPIC_IDENTITY_TOKEN_FILE");
      if (p) u.authentication.identity_token = { source: "file", path: p };
    }
    if (!u.authentication.federation_rule_id) u.authentication.federation_rule_id = ge("ANTHROPIC_FEDERATION_RULE_ID") ?? "";
    (r = u.authentication).service_account_id ?? (r.service_account_id = ge("ANTHROPIC_SERVICE_ACCOUNT_ID"));
  }
  return { config: u, fromFile: true };
};
var vk = async (e, t) => {
  if (e?.authentication.credentials_path) return e.authentication.credentials_path;
  let r = await Bg();
  if (!r) return null;
  let o = t ?? await Sk();
  if (!o) return null;
  return bk(o), (await import("node:path")).join(r, "credentials", `${o}.json`);
};
var Bg = async () => {
  if (!h1()) return null;
  let e = await import("node:path"), t = ge("ANTHROPIC_CONFIG_DIR");
  if (t) return t;
  if (oa()["X-Stainless-OS"] === "Windows") {
    let i = ge("APPDATA");
    if (i) return e.join(i, "Anthropic");
    let s = ge("USERPROFILE");
    if (s) return e.join(s, "AppData", "Roaming", "Anthropic");
    return null;
  }
  let o = ge("XDG_CONFIG_HOME");
  if (o) return e.join(o, "anthropic");
  let n = ge("HOME");
  if (n) return e.join(n, ".config", "anthropic");
  return null;
};
var h1 = () => {
  let e = oa()["X-Stainless-Runtime"];
  return e === "node" || e === "deno";
};
var Sk = async () => {
  let e = await Bg();
  if (!e) return null;
  let t = ge("ANTHROPIC_PROFILE");
  if (t) return t;
  let r = await import("node:fs"), n = (await import("node:path")).join(e, "active_config");
  try {
    return (await r.promises.readFile(n, "utf-8")).trim() || "default";
  } catch (i) {
    if (i?.code !== "ENOENT") throw Error(`failed to read ${n}: ${i}`);
    return "default";
  }
};
function Hg(e) {
  if (!e) throw new z("Identity token file path is empty");
  return async () => {
    let t = await import("node:fs"), r;
    try {
      r = await t.promises.readFile(e, "utf-8");
    } catch (n) {
      throw new z(`Failed to read identity token file at ${e}: ${n}`);
    }
    let o = r.trim();
    if (!o) throw new z(`Identity token file at ${e} is empty`);
    return o;
  };
}
function xk(e) {
  if (!e) throw new z("Identity token value is empty");
  return () => e;
}
function wk(e) {
  return async () => {
    Pu(e.baseURL);
    let t = await e.identityTokenProvider();
    if (t.length > 16384) throw new he(`Identity token is ${Math.ceil(t.length / 1024)} KiB, exceeds the 16 KiB assertion limit`);
    let r = { grant_type: lk, assertion: t, federation_rule_id: e.federationRuleId, organization_id: e.organizationId };
    if (e.serviceAccountId) r.service_account_id = e.serviceAccountId;
    if (e.workspaceId) r.workspace_id = e.workspaceId;
    let o = `${e.baseURL}${Tu}`, n;
    try {
      n = await e.fetch(o, { method: "POST", headers: { "Content-Type": "application/json", "anthropic-beta": `${ro},${dk}`, "User-Agent": e.userAgent || `anthropic-sdk-typescript/${Zt} oidcFederationProvider` }, body: JSON.stringify(r) });
    } catch (c) {
      throw new he(`Failed to reach token endpoint ${o}: ${c}`);
    }
    let i = n.headers.get("Request-Id");
    if (!n.ok) {
      let c = await n.text().catch(() => ""), u = bt(c), d = "";
      if (n.status === 401) d = ` Ensure your federation rule matches your identity token. ${e.workspaceId ? "" : "If your federation rule is scoped to multiple workspaces, set the ANTHROPIC_WORKSPACE_ID environment variable, the 'workspace_id' config key, or the `workspaceId` option. "}View your authentication events in the Workload identity page of Claude Console for more details.`;
      throw new he(`Token exchange failed with status ${n.status}${i ? ` (request-id ${i})` : ""}: ${u}${d}`, n.status, u, i);
    }
    let s = await Iu(n, i), a = Number(s.expires_in);
    if (!Number.isFinite(a)) throw new he(`Token endpoint response missing required fields: ${JSON.stringify(bt(s))}`, n.status, bt(s), i);
    return { token: s.access_token, expiresAt: or() + a };
  };
}
function kk(e) {
  return async (t) => {
    let r = await import("node:fs");
    await Ru(e.credentialsPath, e.onSafetyWarning);
    let o;
    try {
      o = await r.promises.readFile(e.credentialsPath, "utf-8");
    } catch (y) {
      throw new he(`Credentials file not found at ${e.credentialsPath}: ${y}`);
    }
    let n;
    try {
      n = JSON.parse(o);
    } catch (y) {
      throw new he(`Credentials file at ${e.credentialsPath} is not valid JSON: ${y}`);
    }
    let i = n.access_token;
    if (!i) throw new he(`Credentials file at ${e.credentialsPath} must include 'access_token'`);
    let s = n.expires_at;
    if (!t?.forceRefresh && (s == null || or() < s - ni)) return { token: i, expiresAt: s ?? null };
    let a = n.refresh_token;
    if (!e.clientId || !a) throw new he(`Access token at ${e.credentialsPath} has expired and no refresh is available (client_id ${e.clientId ? "set" : "empty"}, refresh_token ${a ? "set" : "empty"})`);
    Pu(e.baseURL);
    let c = { grant_type: uk, refresh_token: a, client_id: e.clientId }, u = `${e.baseURL}${Tu}`, d;
    try {
      d = await e.fetch(u, { method: "POST", headers: { "Content-Type": "application/json", "anthropic-beta": ro, "User-Agent": e.userAgent || `anthropic-sdk-typescript/${Zt} userOAuthProvider` }, body: JSON.stringify(c) });
    } catch (y) {
      throw new he(`User OAuth refresh failed to reach token endpoint: ${y}`);
    }
    let p = d.headers.get("Request-Id");
    if (!d.ok) {
      let y = await d.text().catch(() => "");
      throw new he(`User OAuth refresh failed (HTTP ${d.status}): ${bt(y)}`, d.status, bt(y), p);
    }
    let f = await Iu(d, p), m = Number(f.expires_in);
    if (!Number.isFinite(m)) throw new he(`User OAuth refresh response missing or invalid expires_in: ${JSON.stringify(bt(f))}`, d.status, bt(f), p);
    let g = or() + m, h = f.refresh_token || a;
    return await $u(e.credentialsPath, { ...n, version: Cu, type: "oauth_token", access_token: f.access_token, expires_at: g, refresh_token: h }), { token: f.access_token, expiresAt: g };
  };
}
function qg(e, t) {
  let r = e.authentication.credentials_path ?? null, o = (e.base_url || t.baseURL).replace(/\/+$/, ""), n = y1(e, r, o, t), i = {};
  if (e.workspace_id && e.authentication.type === "user_oauth") i["anthropic-workspace-id"] = e.workspace_id;
  return { provider: n, extraHeaders: i, baseURL: e.base_url || void 0 };
}
async function Ek(e, t) {
  let r = await _k(t);
  if (!r) return null;
  let { config: o, fromFile: n } = r, i = o.authentication.credentials_path || !n ? o : { ...o, authentication: { ...o.authentication, credentials_path: await vk(o, t) ?? void 0 } };
  return qg(i, e);
}
function y1(e, t, r, o) {
  switch (e.authentication.type) {
    case "oidc_federation": {
      let n = e.authentication, i = b1(n);
      if (!i) throw new he("oidc_federation config requires an identity token (set authentication.identity_token, ANTHROPIC_IDENTITY_TOKEN_FILE, or ANTHROPIC_IDENTITY_TOKEN)");
      if (!n.federation_rule_id) throw new he("oidc_federation config requires 'federation_rule_id'. Set it in authentication.federation_rule_id in your profile, or via ANTHROPIC_FEDERATION_RULE_ID (profile takes precedence).");
      if (!e.organization_id) throw new he("oidc_federation config requires organization_id (set ANTHROPIC_ORGANIZATION_ID or config.organization_id)");
      let s = wk({ identityTokenProvider: i, federationRuleId: n.federation_rule_id, organizationId: e.organization_id, serviceAccountId: n.service_account_id, workspaceId: e.workspace_id, baseURL: r, fetch: o.fetch, userAgent: o.userAgent });
      if (t) return _1(s, t, o.onCacheWriteError, o.onSafetyWarning);
      return s;
    }
    case "user_oauth": {
      if (!t) throw new he("user_oauth config requires authentication.credentials_path (or load via a profile so it defaults to <config_dir>/credentials/<profile>.json)");
      return kk({ credentialsPath: t, clientId: e.authentication.client_id, baseURL: r, fetch: o.fetch, userAgent: o.userAgent, onSafetyWarning: o.onSafetyWarning });
    }
    default: {
      let n = e.authentication.type;
      throw new he(`authentication.type "${n}" is not a known authentication type`);
    }
  }
}
function b1(e) {
  if (e.identity_token) {
    let o = e.identity_token.source;
    if (o !== "file") throw new he(`identity_token.source "${o}" is not supported by this SDK version (only "file")`);
    if (!e.identity_token.path) throw new he('identity_token.source "file" requires a non-empty path');
    return Hg(e.identity_token.path);
  }
  let t = ge("ANTHROPIC_IDENTITY_TOKEN_FILE");
  if (t) return Hg(t);
  let r = ge("ANTHROPIC_IDENTITY_TOKEN");
  if (r) return xk(r);
  return null;
}
function _1(e, t, r, o) {
  return async (n) => {
    let i = await import("node:fs");
    await Ru(t, o);
    let s;
    try {
      let c = await i.promises.readFile(t, "utf-8");
      s = JSON.parse(c);
      let u = s?.access_token;
      if (u && !n?.forceRefresh) {
        let d = s?.expires_at;
        if (d == null || or() < d - ni) return { token: u, expiresAt: d ?? null };
      }
    } catch (c) {
      if (c?.code !== "ENOENT" && !(c instanceof SyntaxError)) r?.(c);
    }
    let a = await e(n);
    try {
      await $u(t, { ...s ?? {}, version: Cu, type: "oauth_token", access_token: a.token, expires_at: a.expiresAt });
    } catch (c) {
      r?.(c);
    }
    return a;
  };
}
var At;
var Ct;
var gn = class {
  constructor() {
    At.set(this, void 0), Ct.set(this, void 0), N(this, At, new Uint8Array(), "f"), N(this, Ct, null, "f");
  }
  decode(e) {
    if (e == null) return [];
    let t = e instanceof ArrayBuffer ? new Uint8Array(e) : typeof e === "string" ? oi(e) : e;
    N(this, At, hk([_(this, At, "f"), t]), "f");
    let r = [], o;
    while ((o = v1(_(this, At, "f"), _(this, Ct, "f"))) != null) {
      if (o.carriage && _(this, Ct, "f") == null) {
        N(this, Ct, o.index, "f");
        continue;
      }
      if (_(this, Ct, "f") != null && (o.index !== _(this, Ct, "f") + 1 || o.carriage)) {
        r.push(Lg(_(this, At, "f").subarray(0, _(this, Ct, "f") - 1))), N(this, At, _(this, At, "f").subarray(_(this, Ct, "f")), "f"), N(this, Ct, null, "f");
        continue;
      }
      let n = _(this, Ct, "f") !== null ? o.preceding - 1 : o.preceding, i = Lg(_(this, At, "f").subarray(0, n));
      r.push(i), N(this, At, _(this, At, "f").subarray(o.index), "f"), N(this, Ct, null, "f");
    }
    return r;
  }
  flush() {
    if (!_(this, At, "f").length) return [];
    return this.decode(`
`);
  }
};
At = /* @__PURE__ */ new WeakMap(), Ct = /* @__PURE__ */ new WeakMap();
gn.NEWLINE_CHARS = /* @__PURE__ */ new Set([`
`, "\r"]);
gn.NEWLINE_REGEXP = /\r\n|[\n\r]/g;
function v1(e, t) {
  for (let n = t ?? 0; n < e.length; n++) {
    if (e[n] === 10) return { preceding: n, index: n + 1, carriage: false };
    if (e[n] === 13) return { preceding: n, index: n + 1, carriage: true };
  }
  return null;
}
function Tk(e) {
  for (let o = 0; o < e.length - 1; o++) {
    if (e[o] === 10 && e[o + 1] === 10) return o + 2;
    if (e[o] === 13 && e[o + 1] === 13) return o + 2;
    if (e[o] === 13 && e[o + 1] === 10 && o + 3 < e.length && e[o + 2] === 13 && e[o + 3] === 10) return o + 4;
  }
  return -1;
}
var aa;
var Mt = class _Mt {
  constructor(e, t, r) {
    this.iterator = e, aa.set(this, void 0), this.controller = t, N(this, aa, r, "f");
  }
  static fromSSEResponse(e, t, r) {
    let o = false, n = r ? Fe(r) : console;
    async function* i() {
      if (o) throw new z("Cannot iterate over a consumed stream, use `.tee()` to split the stream.");
      o = true;
      let s = false;
      try {
        for await (let a of S1(e, t)) {
          if (a.event === "completion") try {
            yield JSON.parse(a.data);
          } catch (c) {
            throw n.error("Could not parse message into JSON:", a.data), n.error("From chunk:", a.raw), c;
          }
          if (a.event === "message_start" || a.event === "message_delta" || a.event === "message_stop" || a.event === "content_block_start" || a.event === "content_block_delta" || a.event === "content_block_stop" || a.event === "message" || a.event === "user.message" || a.event === "user.interrupt" || a.event === "user.tool_confirmation" || a.event === "user.custom_tool_result" || a.event === "agent.message" || a.event === "agent.thinking" || a.event === "agent.tool_use" || a.event === "agent.tool_result" || a.event === "agent.mcp_tool_use" || a.event === "agent.mcp_tool_result" || a.event === "agent.custom_tool_use" || a.event === "agent.thread_context_compacted" || a.event === "session.status_running" || a.event === "session.status_idle" || a.event === "session.status_rescheduled" || a.event === "session.status_terminated" || a.event === "session.error" || a.event === "session.deleted" || a.event === "span.model_request_start" || a.event === "span.model_request_end") try {
            yield JSON.parse(a.data);
          } catch (c) {
            throw n.error("Could not parse message into JSON:", a.data), n.error("From chunk:", a.raw), c;
          }
          if (a.event === "ping") continue;
          if (a.event === "error") {
            let c = ku(a.data) ?? a.data, u = c?.error?.type;
            throw new We(void 0, c, void 0, e.headers, u);
          }
        }
        s = true;
      } catch (a) {
        if (Mr(a)) return;
        throw a;
      } finally {
        if (!s) t.abort();
      }
    }
    return new _Mt(i, t, r);
  }
  static fromReadableStream(e, t, r) {
    let o = false;
    async function* n() {
      let s = new gn(), a = ia(e);
      for await (let c of a) for (let u of s.decode(c)) yield u;
      for (let c of s.flush()) yield c;
    }
    async function* i() {
      if (o) throw new z("Cannot iterate over a consumed stream, use `.tee()` to split the stream.");
      o = true;
      let s = false;
      try {
        for await (let a of n()) {
          if (s) continue;
          if (a) yield JSON.parse(a);
        }
        s = true;
      } catch (a) {
        if (Mr(a)) return;
        throw a;
      } finally {
        if (!s) t.abort();
      }
    }
    return new _Mt(i, t, r);
  }
  [(aa = /* @__PURE__ */ new WeakMap(), Symbol.asyncIterator)]() {
    return this.iterator();
  }
  tee() {
    let e = [], t = [], r = this.iterator(), o = (n) => ({ next: () => {
      if (n.length === 0) {
        let i = r.next();
        e.push(i), t.push(i);
      }
      return n.shift();
    } });
    return [new _Mt(() => o(e), this.controller, _(this, aa, "f")), new _Mt(() => o(t), this.controller, _(this, aa, "f"))];
  }
  toReadableStream() {
    let e = this, t;
    return jg({ async start() {
      t = e[Symbol.asyncIterator]();
    }, async pull(r) {
      try {
        let { value: o, done: n } = await t.next();
        if (n) return r.close();
        let i = oi(JSON.stringify(o) + `
`);
        r.enqueue(i);
      } catch (o) {
        r.error(o);
      }
    }, async cancel() {
      await t.return?.();
    } });
  }
};
async function* S1(e, t) {
  if (!e.body) {
    if (t.abort(), typeof globalThis.navigator < "u" && globalThis.navigator.product === "ReactNative") throw new z("The default react-native fetch implementation does not support streaming. Please use expo/fetch: https://docs.expo.dev/versions/latest/sdk/expo/#expofetch-api");
    throw new z("Attempted to iterate over a response with no body");
  }
  let r = new Pk(), o = new gn(), n = ia(e.body);
  for await (let i of x1(n)) for (let s of o.decode(i)) {
    let a = r.decode(s);
    if (a) yield a;
  }
  for (let i of o.flush()) {
    let s = r.decode(i);
    if (s) yield s;
  }
}
async function* x1(e) {
  let t = new Uint8Array();
  for await (let r of e) {
    if (r == null) continue;
    let o = r instanceof ArrayBuffer ? new Uint8Array(r) : typeof r === "string" ? oi(r) : r, n = new Uint8Array(t.length + o.length);
    n.set(t), n.set(o, t.length), t = n;
    let i;
    while ((i = Tk(t)) !== -1) yield t.slice(0, i), t = t.slice(i);
  }
  if (t.length > 0) yield t;
}
var Pk = class {
  constructor() {
    this.event = null, this.data = [], this.chunks = [];
  }
  decode(e) {
    if (e.endsWith("\r")) e = e.substring(0, e.length - 1);
    if (!e) {
      if (!this.event && !this.data.length) return null;
      let n = { event: this.event, data: this.data.join(`
`), raw: this.chunks };
      return this.event = null, this.data = [], this.chunks = [], n;
    }
    if (this.chunks.push(e), e.startsWith(":")) return null;
    let [t, r, o] = w1(e, ":");
    if (o.startsWith(" ")) o = o.substring(1);
    if (t === "event") this.event = o;
    else if (t === "data") this.data.push(o);
    return null;
  }
};
function w1(e, t) {
  let r = e.indexOf(t);
  if (r !== -1) return [e.substring(0, r), t, e.substring(r + t.length)];
  return [e, "", ""];
}
async function Mu(e, t) {
  let { response: r, requestLogID: o, retryOfRequestLogID: n, startTime: i } = t, s = await (async () => {
    if (t.options.stream) {
      if (Fe(e).debug("response", r.status, r.url, r.headers, r.body), t.options.__streamClass) return t.options.__streamClass.fromSSEResponse(r, t.controller);
      return Mt.fromSSEResponse(r, t.controller);
    }
    if (r.status === 204) return null;
    if (t.options.__binaryResponse) return r;
    let c = r.headers.get("content-type")?.split(";")[0]?.trim();
    if (c?.includes("application/json") || c?.endsWith("+json")) {
      if (r.headers.get("content-length") === "0") return;
      let f = await r.json();
      return Zg(f, r);
    }
    return await r.text();
  })();
  return Fe(e).debug(`[${o}] response parsed`, Dr({ retryOfRequestLogID: n, url: r.url, status: r.status, body: s, durationMs: Date.now() - i })), s;
}
function Zg(e, t) {
  if (!e || typeof e !== "object" || Array.isArray(e)) return e;
  return Object.defineProperty(e, "_request_id", { value: t.headers.get("request-id"), enumerable: false });
}
var ca;
var no = class _no extends Promise {
  constructor(e, t, r = Mu) {
    super((o) => {
      o(null);
    });
    this.responsePromise = t, this.parseResponse = r, ca.set(this, void 0), N(this, ca, e, "f");
  }
  _thenUnwrap(e) {
    return new _no(_(this, ca, "f"), this.responsePromise, async (t, r) => Zg(e(await this.parseResponse(t, r), r), r.response));
  }
  asResponse() {
    return this.responsePromise.then((e) => e.response);
  }
  async withResponse() {
    let [e, t] = await Promise.all([this.parse(), this.asResponse()]);
    return { data: e, response: t, request_id: t.headers.get("request-id") };
  }
  parse() {
    if (!this.parsedPromise) this.parsedPromise = this.responsePromise.then((e) => this.parseResponse(_(this, ca, "f"), e));
    return this.parsedPromise;
  }
  then(e, t) {
    return this.parse().then(e, t);
  }
  catch(e) {
    return this.parse().catch(e);
  }
  finally(e) {
    return this.parse().finally(e);
  }
};
ca = /* @__PURE__ */ new WeakMap();
var Du;
var Vg = class {
  constructor(e, t, r, o) {
    Du.set(this, void 0), N(this, Du, e, "f"), this.options = o, this.response = t, this.body = r;
  }
  hasNextPage() {
    if (!this.getPaginatedItems().length) return false;
    return this.nextPageRequestOptions() != null;
  }
  async getNextPage() {
    let e = this.nextPageRequestOptions();
    if (!e) throw new z("No next page expected; please check `.hasNextPage()` before calling `.getNextPage()`.");
    return await _(this, Du, "f").requestAPIList(this.constructor, e);
  }
  async *iterPages() {
    let e = this;
    yield e;
    while (e.hasNextPage()) e = await e.getNextPage(), yield e;
  }
  async *[(Du = /* @__PURE__ */ new WeakMap(), Symbol.asyncIterator)]() {
    for await (let e of this.iterPages()) for (let t of e.getPaginatedItems()) yield t;
  }
};
var Nu = class extends no {
  constructor(e, t, r) {
    super(e, t, async (o, n) => new r(o, n.response, await Mu(o, n), n.options));
  }
  async *[Symbol.asyncIterator]() {
    let e = await this;
    for await (let t of e) yield t;
  }
};
var ir = class extends Vg {
  constructor(e, t, r, o) {
    super(e, t, r, o);
    this.data = r.data || [], this.has_more = r.has_more || false, this.first_id = r.first_id || null, this.last_id = r.last_id || null;
  }
  getPaginatedItems() {
    return this.data ?? [];
  }
  hasNextPage() {
    if (this.has_more === false) return false;
    return super.hasNextPage();
  }
  nextPageRequestOptions() {
    if (this.options.query?.before_id) {
      let t = this.first_id;
      if (!t) return null;
      return { ...this.options, query: { ...wu(this.options.query), before_id: t } };
    }
    let e = this.last_id;
    if (!e) return null;
    return { ...this.options, query: { ...wu(this.options.query), after_id: e } };
  }
};
var ye = class extends Vg {
  constructor(e, t, r, o) {
    super(e, t, r, o);
    this.data = r.data || [], this.next_page = r.next_page || null;
  }
  getPaginatedItems() {
    return this.data ?? [];
  }
  nextPageRequestOptions() {
    let e = this.next_page;
    if (!e) return null;
    return { ...this.options, query: { ...wu(this.options.query), page: e } };
  }
};
var Kg = () => {
  if (typeof File > "u") {
    let { process: e } = globalThis, t = typeof e?.versions?.node === "string" && parseInt(e.versions.node.split(".")) < 20;
    throw Error("`File` is not defined as a global, which is required for file uploads." + (t ? " Update to Node 20 LTS or newer, or set `globalThis.File` to `import('node:buffer').File`." : ""));
  }
};
function oo(e, t, r) {
  return Kg(), new File(e, t ?? "unknown_file", r);
}
function la(e, t) {
  let r = typeof e === "object" && e !== null && ("name" in e && e.name && String(e.name) || "url" in e && e.url && String(e.url) || "filename" in e && e.filename && String(e.filename) || "path" in e && e.path && String(e.path)) || "";
  return t ? r.split(/[\\/]/).pop() || void 0 : r;
}
var Gg = (e) => e != null && typeof e === "object" && typeof e[Symbol.asyncIterator] === "function";
var ii = async (e, t, r = true) => ({ ...e, body: await T1(e.body, t, r) });
var Ik = /* @__PURE__ */ new WeakMap();
function E1(e) {
  let t = typeof e === "function" ? e : e.fetch, r = Ik.get(t);
  if (r) return r;
  let o = (async () => {
    try {
      let n = "Response" in t ? t.Response : (await t("data:,")).constructor, i = new FormData();
      if (i.toString() === await new n(i).text()) return false;
      return true;
    } catch {
      return true;
    }
  })();
  return Ik.set(t, o), o;
}
var T1 = async (e, t, r = true) => {
  if (!await E1(t)) throw TypeError("The provided fetch function does not support file uploads with the current global FormData class.");
  let o = new FormData();
  return await Promise.all(Object.entries(e || {}).map(([n, i]) => Wg(o, n, i, r))), o;
};
var P1 = (e) => e instanceof Blob && "name" in e;
var Wg = async (e, t, r, o) => {
  if (r === void 0) return;
  if (r == null) throw TypeError(`Received null for "${t}"; to pass null in FormData, you must use the string 'null'`);
  if (typeof r === "string" || typeof r === "number" || typeof r === "boolean") e.append(t, String(r));
  else if (r instanceof Response) {
    let n = {}, i = r.headers.get("Content-Type");
    if (i) n = { type: i };
    e.append(t, oo([await r.blob()], la(r, o), n));
  } else if (Gg(r)) e.append(t, oo([await new Response(Eu(r)).blob()], la(r, o)));
  else if (P1(r)) e.append(t, oo([r], la(r, o), { type: r.type }));
  else if (Array.isArray(r)) await Promise.all(r.map((n) => Wg(e, t + "[]", n, o)));
  else if (typeof r === "object") await Promise.all(Object.entries(r).map(([n, i]) => Wg(e, `${t}[${n}]`, i, o)));
  else throw TypeError(`Invalid value given to form, expected a string, number, boolean, object, Array, File or Blob but got ${r} instead`);
};
var Rk = (e) => e != null && typeof e === "object" && typeof e.size === "number" && typeof e.type === "string" && typeof e.text === "function" && typeof e.slice === "function" && typeof e.arrayBuffer === "function";
var I1 = (e) => e != null && typeof e === "object" && typeof e.name === "string" && typeof e.lastModified === "number" && Rk(e);
var R1 = (e) => e != null && typeof e === "object" && typeof e.url === "string" && typeof e.blob === "function";
async function ju(e, t, r) {
  if (Kg(), e = await e, t || (t = la(e, true)), I1(e)) {
    if (e instanceof File && t == null && r == null) return e;
    return oo([await e.arrayBuffer()], t ?? e.name, { type: e.type, lastModified: e.lastModified, ...r });
  }
  if (R1(e)) {
    let n = await e.blob();
    return t || (t = new URL(e.url).pathname.split(/[\\/]/).pop()), oo(await Jg(n), t, r);
  }
  let o = await Jg(e);
  if (!r?.type) {
    let n = o.find((i) => typeof i === "object" && "type" in i && i.type);
    if (typeof n === "string") r = { ...r, type: n };
  }
  return oo(o, t, r);
}
async function Jg(e) {
  let t = [];
  if (typeof e === "string" || ArrayBuffer.isView(e) || e instanceof ArrayBuffer) t.push(e);
  else if (Rk(e)) t.push(e instanceof Blob ? e : await e.arrayBuffer());
  else if (Gg(e)) for await (let r of e) t.push(...await Jg(r));
  else {
    let r = e?.constructor?.name;
    throw Error(`Unexpected data type: ${typeof e}${r ? `; constructor: ${r}` : ""}${$1(e)}`);
  }
  return t;
}
function $1(e) {
  if (typeof e !== "object" || e === null) return "";
  return `; props: [${Object.getOwnPropertyNames(e).map((r) => `"${r}"`).join(", ")}]`;
}
var J = class {
  constructor(e) {
    this._client = e;
  }
};
var $k = Symbol.for("brand.privateNullableHeaders");
function* A1(e) {
  if (!e) return;
  if ($k in e) {
    let { values: o, nulls: n } = e;
    yield* o.entries();
    for (let i of n) yield [i, null];
    return;
  }
  let t = false, r;
  if (e instanceof Headers) r = e.entries();
  else if (Dg(e)) r = e;
  else t = true, r = Object.entries(e ?? {});
  for (let o of r) {
    let n = o[0];
    if (typeof n !== "string") throw TypeError("expected header name to be a string");
    let i = Dg(o[1]) ? o[1] : [o[1]], s = false;
    for (let a of i) {
      if (a === void 0) continue;
      if (t && !s) s = true, yield [n, null];
      yield [n, a];
    }
  }
}
var E = (e) => {
  let t = new Headers(), r = /* @__PURE__ */ new Set();
  for (let o of e) {
    let n = /* @__PURE__ */ new Set();
    for (let [i, s] of A1(o)) {
      let a = i.toLowerCase();
      if (!n.has(a)) t.delete(i), n.add(a);
      if (s === null) t.delete(i), r.add(a);
      else t.append(i, s), r.delete(a);
    }
  }
  return { [$k]: true, values: t, nulls: r };
};
function Ak(e) {
  return e.replace(/[^A-Za-z0-9\-._~!$&'()*+,;=:@]+/g, encodeURIComponent);
}
var Ok = Object.freeze(/* @__PURE__ */ Object.create(null));
var C1 = (e = Ak) => function(r, ...o) {
  if (r.length === 1) return r[0];
  let n = false, i = [], s = r.reduce((d, p, f) => {
    if (/[?#]/.test(p)) n = true;
    let m = o[f], g = (n ? encodeURIComponent : e)("" + m);
    if (f !== o.length && (m == null || typeof m === "object" && m.toString === Object.getPrototypeOf(Object.getPrototypeOf(m.hasOwnProperty ?? Ok) ?? Ok)?.toString)) g = m + "", i.push({ start: d.length + p.length, length: g.length, error: `Value of type ${Object.prototype.toString.call(m).slice(8, -1)} is not a valid path parameter` });
    return d + p + (f === o.length ? "" : g);
  }, ""), a = s.split(/[?#]/, 1)[0], c = /(?<=^|\/)(?:\.|%2e){1,2}(?=\/|$)/gi, u;
  while ((u = c.exec(a)) !== null) i.push({ start: u.index, length: u[0].length, error: `Value "${u[0]}" can't be safely passed as a path parameter` });
  if (i.sort((d, p) => d.start - p.start), i.length > 0) {
    let d = 0, p = i.reduce((f, m) => {
      let g = " ".repeat(m.start - d), h = "^".repeat(m.length);
      return d = m.start + m.length, f + g + h;
    }, "");
    throw new z(`Path parameters result in path with invalid segments:
${i.map((f) => f.error).join(`
`)}
${s}
${p}`);
  }
  return s;
};
var M = C1(Ak);
var ua = class extends J {
  create(e, t) {
    let { betas: r, ...o } = e;
    return this._client.post("/v1/environments?beta=true", { body: o, ...t, headers: E([{ "anthropic-beta": [...r ?? [], "managed-agents-2026-04-01"].toString() }, t?.headers]) });
  }
  retrieve(e, t = {}, r) {
    let { betas: o } = t ?? {};
    return this._client.get(M`/v1/environments/${e}?beta=true`, { ...r, headers: E([{ "anthropic-beta": [...o ?? [], "managed-agents-2026-04-01"].toString() }, r?.headers]) });
  }
  update(e, t, r) {
    let { betas: o, ...n } = t;
    return this._client.post(M`/v1/environments/${e}?beta=true`, { body: n, ...r, headers: E([{ "anthropic-beta": [...o ?? [], "managed-agents-2026-04-01"].toString() }, r?.headers]) });
  }
  list(e = {}, t) {
    let { betas: r, ...o } = e ?? {};
    return this._client.getAPIList("/v1/environments?beta=true", ye, { query: o, ...t, headers: E([{ "anthropic-beta": [...r ?? [], "managed-agents-2026-04-01"].toString() }, t?.headers]) });
  }
  delete(e, t = {}, r) {
    let { betas: o } = t ?? {};
    return this._client.delete(M`/v1/environments/${e}?beta=true`, { ...r, headers: E([{ "anthropic-beta": [...o ?? [], "managed-agents-2026-04-01"].toString() }, r?.headers]) });
  }
  archive(e, t = {}, r) {
    let { betas: o } = t ?? {};
    return this._client.post(M`/v1/environments/${e}/archive?beta=true`, { ...r, headers: E([{ "anthropic-beta": [...o ?? [], "managed-agents-2026-04-01"].toString() }, r?.headers]) });
  }
};
var da = Symbol("anthropic.sdk.stainlessHelper");
function Uu(e) {
  return typeof e === "object" && e !== null && da in e;
}
function Xg(e, t) {
  let r = /* @__PURE__ */ new Set();
  if (e) {
    for (let o of e) if (Uu(o)) r.add(o[da]);
  }
  if (t) for (let o of t) {
    if (Uu(o)) r.add(o[da]);
    if (Array.isArray(o.content)) {
      for (let n of o.content) if (Uu(n)) r.add(n[da]);
    }
  }
  return Array.from(r);
}
function zu(e, t) {
  let r = Xg(e, t);
  if (r.length === 0) return {};
  return { "x-stainless-helper": r.join(", ") };
}
function Ck(e) {
  if (Uu(e)) return { "x-stainless-helper": e[da] };
  return {};
}
var pa = class extends J {
  list(e = {}, t) {
    let { betas: r, ...o } = e ?? {};
    return this._client.getAPIList("/v1/files?beta=true", ir, { query: o, ...t, headers: E([{ "anthropic-beta": [...r ?? [], "files-api-2025-04-14"].toString() }, t?.headers]) });
  }
  delete(e, t = {}, r) {
    let { betas: o } = t ?? {};
    return this._client.delete(M`/v1/files/${e}?beta=true`, { ...r, headers: E([{ "anthropic-beta": [...o ?? [], "files-api-2025-04-14"].toString() }, r?.headers]) });
  }
  download(e, t = {}, r) {
    let { betas: o } = t ?? {};
    return this._client.get(M`/v1/files/${e}/content?beta=true`, { ...r, headers: E([{ "anthropic-beta": [...o ?? [], "files-api-2025-04-14"].toString(), Accept: "application/binary" }, r?.headers]), __binaryResponse: true });
  }
  retrieveMetadata(e, t = {}, r) {
    let { betas: o } = t ?? {};
    return this._client.get(M`/v1/files/${e}?beta=true`, { ...r, headers: E([{ "anthropic-beta": [...o ?? [], "files-api-2025-04-14"].toString() }, r?.headers]) });
  }
  upload(e, t) {
    let { betas: r, ...o } = e;
    return this._client.post("/v1/files?beta=true", ii({ body: o, ...t, headers: E([{ "anthropic-beta": [...r ?? [], "files-api-2025-04-14"].toString() }, Ck(o.file), t?.headers]) }, this._client));
  }
};
var fa = class extends J {
  retrieve(e, t = {}, r) {
    let { betas: o } = t ?? {};
    return this._client.get(M`/v1/models/${e}?beta=true`, { ...r, headers: E([{ ...o?.toString() != null ? { "anthropic-beta": o?.toString() } : void 0 }, r?.headers]) });
  }
  list(e = {}, t) {
    let { betas: r, ...o } = e ?? {};
    return this._client.getAPIList("/v1/models?beta=true", ir, { query: o, ...t, headers: E([{ ...r?.toString() != null ? { "anthropic-beta": r?.toString() } : void 0 }, t?.headers]) });
  }
};
var ma = class extends J {
  create(e, t) {
    let { betas: r, ...o } = e;
    return this._client.post("/v1/user_profiles?beta=true", { body: o, ...t, headers: E([{ "anthropic-beta": [...r ?? [], "user-profiles-2026-03-24"].toString() }, t?.headers]) });
  }
  retrieve(e, t = {}, r) {
    let { betas: o } = t ?? {};
    return this._client.get(M`/v1/user_profiles/${e}?beta=true`, { ...r, headers: E([{ "anthropic-beta": [...o ?? [], "user-profiles-2026-03-24"].toString() }, r?.headers]) });
  }
  update(e, t, r) {
    let { betas: o, ...n } = t;
    return this._client.post(M`/v1/user_profiles/${e}?beta=true`, { body: n, ...r, headers: E([{ "anthropic-beta": [...o ?? [], "user-profiles-2026-03-24"].toString() }, r?.headers]) });
  }
  list(e = {}, t) {
    let { betas: r, ...o } = e ?? {};
    return this._client.getAPIList("/v1/user_profiles?beta=true", ye, { query: o, ...t, headers: E([{ "anthropic-beta": [...r ?? [], "user-profiles-2026-03-24"].toString() }, t?.headers]) });
  }
  createEnrollmentURL(e, t = {}, r) {
    let { betas: o } = t ?? {};
    return this._client.post(M`/v1/user_profiles/${e}/enrollment_url?beta=true`, { ...r, headers: E([{ "anthropic-beta": [...o ?? [], "user-profiles-2026-03-24"].toString() }, r?.headers]) });
  }
};
var ga = class extends J {
  list(e, t = {}, r) {
    let { betas: o, ...n } = t ?? {};
    return this._client.getAPIList(M`/v1/agents/${e}/versions?beta=true`, ye, { query: n, ...r, headers: E([{ "anthropic-beta": [...o ?? [], "managed-agents-2026-04-01"].toString() }, r?.headers]) });
  }
};
var si = class extends J {
  constructor() {
    super(...arguments);
    this.versions = new ga(this._client);
  }
  create(e, t) {
    let { betas: r, ...o } = e;
    return this._client.post("/v1/agents?beta=true", { body: o, ...t, headers: E([{ "anthropic-beta": [...r ?? [], "managed-agents-2026-04-01"].toString() }, t?.headers]) });
  }
  retrieve(e, t = {}, r) {
    let { betas: o, ...n } = t ?? {};
    return this._client.get(M`/v1/agents/${e}?beta=true`, { query: n, ...r, headers: E([{ "anthropic-beta": [...o ?? [], "managed-agents-2026-04-01"].toString() }, r?.headers]) });
  }
  update(e, t, r) {
    let { betas: o, ...n } = t;
    return this._client.post(M`/v1/agents/${e}?beta=true`, { body: n, ...r, headers: E([{ "anthropic-beta": [...o ?? [], "managed-agents-2026-04-01"].toString() }, r?.headers]) });
  }
  list(e = {}, t) {
    let { betas: r, ...o } = e ?? {};
    return this._client.getAPIList("/v1/agents?beta=true", ye, { query: o, ...t, headers: E([{ "anthropic-beta": [...r ?? [], "managed-agents-2026-04-01"].toString() }, t?.headers]) });
  }
  archive(e, t = {}, r) {
    let { betas: o } = t ?? {};
    return this._client.post(M`/v1/agents/${e}/archive?beta=true`, { ...r, headers: E([{ "anthropic-beta": [...o ?? [], "managed-agents-2026-04-01"].toString() }, r?.headers]) });
  }
};
si.Versions = ga;
var ha = class extends J {
  create(e, t, r) {
    let { view: o, betas: n, ...i } = t;
    return this._client.post(M`/v1/memory_stores/${e}/memories?beta=true`, { query: { view: o }, body: i, ...r, headers: E([{ "anthropic-beta": [...n ?? [], "managed-agents-2026-04-01"].toString() }, r?.headers]) });
  }
  retrieve(e, t, r) {
    let { memory_store_id: o, betas: n, ...i } = t;
    return this._client.get(M`/v1/memory_stores/${o}/memories/${e}?beta=true`, { query: i, ...r, headers: E([{ "anthropic-beta": [...n ?? [], "managed-agents-2026-04-01"].toString() }, r?.headers]) });
  }
  update(e, t, r) {
    let { memory_store_id: o, view: n, betas: i, ...s } = t;
    return this._client.post(M`/v1/memory_stores/${o}/memories/${e}?beta=true`, { query: { view: n }, body: s, ...r, headers: E([{ "anthropic-beta": [...i ?? [], "managed-agents-2026-04-01"].toString() }, r?.headers]) });
  }
  list(e, t = {}, r) {
    let { betas: o, ...n } = t ?? {};
    return this._client.getAPIList(M`/v1/memory_stores/${e}/memories?beta=true`, ye, { query: n, ...r, headers: E([{ "anthropic-beta": [...o ?? [], "managed-agents-2026-04-01"].toString() }, r?.headers]) });
  }
  delete(e, t, r) {
    let { memory_store_id: o, expected_content_sha256: n, betas: i } = t;
    return this._client.delete(M`/v1/memory_stores/${o}/memories/${e}?beta=true`, { query: { expected_content_sha256: n }, ...r, headers: E([{ "anthropic-beta": [...i ?? [], "managed-agents-2026-04-01"].toString() }, r?.headers]) });
  }
};
var ya = class extends J {
  retrieve(e, t, r) {
    let { memory_store_id: o, betas: n, ...i } = t;
    return this._client.get(M`/v1/memory_stores/${o}/memory_versions/${e}?beta=true`, { query: i, ...r, headers: E([{ "anthropic-beta": [...n ?? [], "managed-agents-2026-04-01"].toString() }, r?.headers]) });
  }
  list(e, t = {}, r) {
    let { betas: o, ...n } = t ?? {};
    return this._client.getAPIList(M`/v1/memory_stores/${e}/memory_versions?beta=true`, ye, { query: n, ...r, headers: E([{ "anthropic-beta": [...o ?? [], "managed-agents-2026-04-01"].toString() }, r?.headers]) });
  }
  redact(e, t, r) {
    let { memory_store_id: o, betas: n } = t;
    return this._client.post(M`/v1/memory_stores/${o}/memory_versions/${e}/redact?beta=true`, { ...r, headers: E([{ "anthropic-beta": [...n ?? [], "managed-agents-2026-04-01"].toString() }, r?.headers]) });
  }
};
var io = class extends J {
  constructor() {
    super(...arguments);
    this.memories = new ha(this._client), this.memoryVersions = new ya(this._client);
  }
  create(e, t) {
    let { betas: r, ...o } = e;
    return this._client.post("/v1/memory_stores?beta=true", { body: o, ...t, headers: E([{ "anthropic-beta": [...r ?? [], "managed-agents-2026-04-01"].toString() }, t?.headers]) });
  }
  retrieve(e, t = {}, r) {
    let { betas: o } = t ?? {};
    return this._client.get(M`/v1/memory_stores/${e}?beta=true`, { ...r, headers: E([{ "anthropic-beta": [...o ?? [], "managed-agents-2026-04-01"].toString() }, r?.headers]) });
  }
  update(e, t, r) {
    let { betas: o, ...n } = t;
    return this._client.post(M`/v1/memory_stores/${e}?beta=true`, { body: n, ...r, headers: E([{ "anthropic-beta": [...o ?? [], "managed-agents-2026-04-01"].toString() }, r?.headers]) });
  }
  list(e = {}, t) {
    let { betas: r, ...o } = e ?? {};
    return this._client.getAPIList("/v1/memory_stores?beta=true", ye, { query: o, ...t, headers: E([{ "anthropic-beta": [...r ?? [], "managed-agents-2026-04-01"].toString() }, t?.headers]) });
  }
  delete(e, t = {}, r) {
    let { betas: o } = t ?? {};
    return this._client.delete(M`/v1/memory_stores/${e}?beta=true`, { ...r, headers: E([{ "anthropic-beta": [...o ?? [], "managed-agents-2026-04-01"].toString() }, r?.headers]) });
  }
  archive(e, t = {}, r) {
    let { betas: o } = t ?? {};
    return this._client.post(M`/v1/memory_stores/${e}/archive?beta=true`, { ...r, headers: E([{ "anthropic-beta": [...o ?? [], "managed-agents-2026-04-01"].toString() }, r?.headers]) });
  }
};
io.Memories = ha;
io.MemoryVersions = ya;
var ai = class _ai {
  constructor(e, t) {
    this.iterator = e, this.controller = t;
  }
  async *decoder() {
    let e = new gn();
    for await (let t of this.iterator) for (let r of e.decode(t)) yield JSON.parse(r);
    for (let t of e.flush()) yield JSON.parse(t);
  }
  [Symbol.asyncIterator]() {
    return this.decoder();
  }
  static fromResponse(e, t) {
    if (!e.body) {
      if (t.abort(), typeof globalThis.navigator < "u" && globalThis.navigator.product === "ReactNative") throw new z("The default react-native fetch implementation does not support streaming. Please use expo/fetch: https://docs.expo.dev/versions/latest/sdk/expo/#expofetch-api");
      throw new z("Attempted to iterate over a response with no body");
    }
    return new _ai(ia(e.body), t);
  }
};
var ba = class extends J {
  create(e, t) {
    let { betas: r, ...o } = e;
    return this._client.post("/v1/messages/batches?beta=true", { body: o, ...t, headers: E([{ "anthropic-beta": [...r ?? [], "message-batches-2024-09-24"].toString() }, t?.headers]) });
  }
  retrieve(e, t = {}, r) {
    let { betas: o } = t ?? {};
    return this._client.get(M`/v1/messages/batches/${e}?beta=true`, { ...r, headers: E([{ "anthropic-beta": [...o ?? [], "message-batches-2024-09-24"].toString() }, r?.headers]) });
  }
  list(e = {}, t) {
    let { betas: r, ...o } = e ?? {};
    return this._client.getAPIList("/v1/messages/batches?beta=true", ir, { query: o, ...t, headers: E([{ "anthropic-beta": [...r ?? [], "message-batches-2024-09-24"].toString() }, t?.headers]) });
  }
  delete(e, t = {}, r) {
    let { betas: o } = t ?? {};
    return this._client.delete(M`/v1/messages/batches/${e}?beta=true`, { ...r, headers: E([{ "anthropic-beta": [...o ?? [], "message-batches-2024-09-24"].toString() }, r?.headers]) });
  }
  cancel(e, t = {}, r) {
    let { betas: o } = t ?? {};
    return this._client.post(M`/v1/messages/batches/${e}/cancel?beta=true`, { ...r, headers: E([{ "anthropic-beta": [...o ?? [], "message-batches-2024-09-24"].toString() }, r?.headers]) });
  }
  async results(e, t = {}, r) {
    let o = await this.retrieve(e);
    if (!o.results_url) throw new z(`No batch \`results_url\`; Has it finished processing? ${o.processing_status} - ${o.id}`);
    let { betas: n } = t ?? {};
    return this._client.get(o.results_url, { ...r, headers: E([{ "anthropic-beta": [...n ?? [], "message-batches-2024-09-24"].toString(), Accept: "application/binary" }, r?.headers]), stream: true, __binaryResponse: true })._thenUnwrap((i, s) => ai.fromResponse(s.response, s.controller));
  }
};
var Lu = { "claude-opus-4-20250514": 8192, "claude-opus-4-0": 8192, "claude-4-opus-20250514": 8192, "anthropic.claude-opus-4-20250514-v1:0": 8192, "claude-opus-4@20250514": 8192, "claude-opus-4-1-20250805": 8192, "anthropic.claude-opus-4-1-20250805-v1:0": 8192, "claude-opus-4-1@20250805": 8192 };
function Mk(e) {
  return e?.output_format ?? e?.output_config?.format;
}
function Yg(e, t, r) {
  let o = Mk(t);
  if (!t || !("parse" in (o ?? {}))) return { ...e, content: e.content.map((n) => {
    if (n.type === "text") {
      let i = Object.defineProperty({ ...n }, "parsed_output", { value: null, enumerable: false });
      return Object.defineProperty(i, "parsed", { get() {
        return r.logger.warn("The `parsed` property on `text` blocks is deprecated, please use `parsed_output` instead."), null;
      }, enumerable: false });
    }
    return n;
  }), parsed_output: null };
  return Qg(e, t, r);
}
function Qg(e, t, r) {
  let o = null, n = e.content.map((i) => {
    if (i.type === "text") {
      let s = q1(t, i.text);
      if (o === null) o = s;
      let a = Object.defineProperty({ ...i }, "parsed_output", { value: s, enumerable: false });
      return Object.defineProperty(a, "parsed", { get() {
        return r.logger.warn("The `parsed` property on `text` blocks is deprecated, please use `parsed_output` instead."), s;
      }, enumerable: false });
    }
    return i;
  });
  return { ...e, content: n, parsed_output: o };
}
function q1(e, t) {
  let r = Mk(e);
  if (r?.type !== "json_schema") return null;
  try {
    if ("parse" in r) return r.parse(t);
    return JSON.parse(t);
  } catch (o) {
    throw new z(`Failed to parse structured output: ${o}`);
  }
}
var Z1 = (e) => {
  let t = 0, r = [];
  while (t < e.length) {
    let o = e[t];
    if (o === "\\") {
      t++;
      continue;
    }
    if (o === "{") {
      r.push({ type: "brace", value: "{" }), t++;
      continue;
    }
    if (o === "}") {
      r.push({ type: "brace", value: "}" }), t++;
      continue;
    }
    if (o === "[") {
      r.push({ type: "paren", value: "[" }), t++;
      continue;
    }
    if (o === "]") {
      r.push({ type: "paren", value: "]" }), t++;
      continue;
    }
    if (o === ":") {
      r.push({ type: "separator", value: ":" }), t++;
      continue;
    }
    if (o === ",") {
      r.push({ type: "delimiter", value: "," }), t++;
      continue;
    }
    if (o === '"') {
      let a = "", c = false;
      o = e[++t];
      while (o !== '"') {
        if (t === e.length) {
          c = true;
          break;
        }
        if (o === "\\") {
          if (t++, t === e.length) {
            c = true;
            break;
          }
          a += o + e[t], o = e[++t];
        } else a += o, o = e[++t];
      }
      if (o = e[++t], !c) r.push({ type: "string", value: a });
      continue;
    }
    if (o && /\s/.test(o)) {
      t++;
      continue;
    }
    let i = /[0-9]/;
    if (o && i.test(o) || o === "-" || o === ".") {
      let a = "";
      if (o === "-") a += o, o = e[++t];
      while (o && i.test(o) || o === ".") a += o, o = e[++t];
      r.push({ type: "number", value: a });
      continue;
    }
    let s = /[a-z]/i;
    if (o && s.test(o)) {
      let a = "";
      while (o && s.test(o)) {
        if (t === e.length) break;
        a += o, o = e[++t];
      }
      if (a == "true" || a == "false" || a === "null") r.push({ type: "name", value: a });
      else {
        t++;
        continue;
      }
      continue;
    }
    t++;
  }
  return r;
};
var ci = (e) => {
  if (e.length === 0) return e;
  let t = e[e.length - 1];
  switch (t.type) {
    case "separator":
      return e = e.slice(0, e.length - 1), ci(e);
      break;
    case "number":
      let r = t.value[t.value.length - 1];
      if (r === "." || r === "-") return e = e.slice(0, e.length - 1), ci(e);
    case "string":
      let o = e[e.length - 2];
      if (o?.type === "delimiter") return e = e.slice(0, e.length - 1), ci(e);
      else if (o?.type === "brace" && o.value === "{") return e = e.slice(0, e.length - 1), ci(e);
      break;
    case "delimiter":
      return e = e.slice(0, e.length - 1), ci(e);
      break;
  }
  return e;
};
var V1 = (e) => {
  let t = [];
  if (e.map((r) => {
    if (r.type === "brace") if (r.value === "{") t.push("}");
    else t.splice(t.lastIndexOf("}"), 1);
    if (r.type === "paren") if (r.value === "[") t.push("]");
    else t.splice(t.lastIndexOf("]"), 1);
  }), t.length > 0) t.reverse().map((r) => {
    if (r === "}") e.push({ type: "brace", value: "}" });
    else if (r === "]") e.push({ type: "paren", value: "]" });
  });
  return e;
};
var W1 = (e) => {
  let t = "";
  return e.map((r) => {
    switch (r.type) {
      case "string":
        t += '"' + r.value + '"';
        break;
      default:
        t += r.value;
        break;
    }
  }), t;
};
var Fu = (e) => JSON.parse(W1(V1(ci(Z1(e)))));
var Vt;
var hn;
var li;
var _a;
var Bu;
var va;
var Sa;
var Hu;
var xa;
var Nr;
var wa;
var qu;
var Zu;
var so;
var Vu;
var Wu;
var ka;
var eh;
var Dk;
var Ku;
var th;
var rh;
var nh;
var Nk;
var jk = "__json_buf";
function Uk(e) {
  return e.type === "tool_use" || e.type === "server_tool_use" || e.type === "mcp_tool_use";
}
var Ea = class _Ea {
  constructor(e, t) {
    Vt.add(this), this.messages = [], this.receivedMessages = [], hn.set(this, void 0), li.set(this, null), this.controller = new AbortController(), _a.set(this, void 0), Bu.set(this, () => {
    }), va.set(this, () => {
    }), Sa.set(this, void 0), Hu.set(this, () => {
    }), xa.set(this, () => {
    }), Nr.set(this, {}), wa.set(this, false), qu.set(this, false), Zu.set(this, false), so.set(this, false), Vu.set(this, void 0), Wu.set(this, void 0), ka.set(this, void 0), Ku.set(this, (r) => {
      if (N(this, qu, true, "f"), Mr(r)) r = new et();
      if (r instanceof et) return N(this, Zu, true, "f"), this._emit("abort", r);
      if (r instanceof z) return this._emit("error", r);
      if (r instanceof Error) {
        let o = new z(r.message);
        return o.cause = r, this._emit("error", o);
      }
      return this._emit("error", new z(String(r)));
    }), N(this, _a, new Promise((r, o) => {
      N(this, Bu, r, "f"), N(this, va, o, "f");
    }), "f"), N(this, Sa, new Promise((r, o) => {
      N(this, Hu, r, "f"), N(this, xa, o, "f");
    }), "f"), _(this, _a, "f").catch(() => {
    }), _(this, Sa, "f").catch(() => {
    }), N(this, li, e, "f"), N(this, ka, t?.logger ?? console, "f");
  }
  get response() {
    return _(this, Vu, "f");
  }
  get request_id() {
    return _(this, Wu, "f");
  }
  async withResponse() {
    N(this, so, true, "f");
    let e = await _(this, _a, "f");
    if (!e) throw Error("Could not resolve a `Response` object");
    return { data: this, response: e, request_id: e.headers.get("request-id") };
  }
  static fromReadableStream(e) {
    let t = new _Ea(null);
    return t._run(() => t._fromReadableStream(e)), t;
  }
  static createMessage(e, t, r, { logger: o } = {}) {
    let n = new _Ea(t, { logger: o });
    for (let i of t.messages) n._addMessageParam(i);
    return N(n, li, { ...t, stream: true }, "f"), n._run(() => n._createMessage(e, { ...t, stream: true }, { ...r, headers: { ...r?.headers, "X-Stainless-Helper-Method": "stream" } })), n;
  }
  _run(e) {
    e().then(() => {
      this._emitFinal(), this._emit("end");
    }, _(this, Ku, "f"));
  }
  _addMessageParam(e) {
    this.messages.push(e);
  }
  _addMessage(e, t = true) {
    if (this.receivedMessages.push(e), t) this._emit("message", e);
  }
  async _createMessage(e, t, r) {
    let o = r?.signal, n;
    if (o) {
      if (o.aborted) this.controller.abort();
      n = this.controller.abort.bind(this.controller), o.addEventListener("abort", n);
    }
    try {
      _(this, Vt, "m", th).call(this);
      let { response: i, data: s } = await e.create({ ...t, stream: true }, { ...r, signal: this.controller.signal }).withResponse();
      this._connected(i);
      for await (let a of s) _(this, Vt, "m", rh).call(this, a);
      if (s.controller.signal?.aborted) throw new et();
      _(this, Vt, "m", nh).call(this);
    } finally {
      if (o && n) o.removeEventListener("abort", n);
    }
  }
  _connected(e) {
    if (this.ended) return;
    N(this, Vu, e, "f"), N(this, Wu, e?.headers.get("request-id"), "f"), _(this, Bu, "f").call(this, e), this._emit("connect");
  }
  get ended() {
    return _(this, wa, "f");
  }
  get errored() {
    return _(this, qu, "f");
  }
  get aborted() {
    return _(this, Zu, "f");
  }
  abort() {
    this.controller.abort();
  }
  on(e, t) {
    return (_(this, Nr, "f")[e] || (_(this, Nr, "f")[e] = [])).push({ listener: t }), this;
  }
  off(e, t) {
    let r = _(this, Nr, "f")[e];
    if (!r) return this;
    let o = r.findIndex((n) => n.listener === t);
    if (o >= 0) r.splice(o, 1);
    return this;
  }
  once(e, t) {
    return (_(this, Nr, "f")[e] || (_(this, Nr, "f")[e] = [])).push({ listener: t, once: true }), this;
  }
  emitted(e) {
    return new Promise((t, r) => {
      if (N(this, so, true, "f"), e !== "error") this.once("error", r);
      this.once(e, t);
    });
  }
  async done() {
    N(this, so, true, "f"), await _(this, Sa, "f");
  }
  get currentMessage() {
    return _(this, hn, "f");
  }
  async finalMessage() {
    return await this.done(), _(this, Vt, "m", eh).call(this);
  }
  async finalText() {
    return await this.done(), _(this, Vt, "m", Dk).call(this);
  }
  _emit(e, ...t) {
    if (_(this, wa, "f")) return;
    if (e === "end") N(this, wa, true, "f"), _(this, Hu, "f").call(this);
    let r = _(this, Nr, "f")[e];
    if (r) _(this, Nr, "f")[e] = r.filter((o) => !o.once), r.forEach(({ listener: o }) => o(...t));
    if (e === "abort") {
      let o = t[0];
      if (!_(this, so, "f") && !r?.length) Promise.reject(o);
      _(this, va, "f").call(this, o), _(this, xa, "f").call(this, o), this._emit("end");
      return;
    }
    if (e === "error") {
      let o = t[0];
      if (!_(this, so, "f") && !r?.length) Promise.reject(o);
      _(this, va, "f").call(this, o), _(this, xa, "f").call(this, o), this._emit("end");
    }
  }
  _emitFinal() {
    if (this.receivedMessages.at(-1)) this._emit("finalMessage", _(this, Vt, "m", eh).call(this));
  }
  async _fromReadableStream(e, t) {
    let r = t?.signal, o;
    if (r) {
      if (r.aborted) this.controller.abort();
      o = this.controller.abort.bind(this.controller), r.addEventListener("abort", o);
    }
    try {
      _(this, Vt, "m", th).call(this), this._connected(null);
      let n = Mt.fromReadableStream(e, this.controller);
      for await (let i of n) _(this, Vt, "m", rh).call(this, i);
      if (n.controller.signal?.aborted) throw new et();
      _(this, Vt, "m", nh).call(this);
    } finally {
      if (r && o) r.removeEventListener("abort", o);
    }
  }
  [(hn = /* @__PURE__ */ new WeakMap(), li = /* @__PURE__ */ new WeakMap(), _a = /* @__PURE__ */ new WeakMap(), Bu = /* @__PURE__ */ new WeakMap(), va = /* @__PURE__ */ new WeakMap(), Sa = /* @__PURE__ */ new WeakMap(), Hu = /* @__PURE__ */ new WeakMap(), xa = /* @__PURE__ */ new WeakMap(), Nr = /* @__PURE__ */ new WeakMap(), wa = /* @__PURE__ */ new WeakMap(), qu = /* @__PURE__ */ new WeakMap(), Zu = /* @__PURE__ */ new WeakMap(), so = /* @__PURE__ */ new WeakMap(), Vu = /* @__PURE__ */ new WeakMap(), Wu = /* @__PURE__ */ new WeakMap(), ka = /* @__PURE__ */ new WeakMap(), Ku = /* @__PURE__ */ new WeakMap(), Vt = /* @__PURE__ */ new WeakSet(), eh = function() {
    if (this.receivedMessages.length === 0) throw new z("stream ended without producing a Message with role=assistant");
    return this.receivedMessages.at(-1);
  }, Dk = function() {
    if (this.receivedMessages.length === 0) throw new z("stream ended without producing a Message with role=assistant");
    let t = this.receivedMessages.at(-1).content.filter((r) => r.type === "text").map((r) => r.text);
    if (t.length === 0) throw new z("stream ended without producing a content block with type=text");
    return t.join(" ");
  }, th = function() {
    if (this.ended) return;
    N(this, hn, void 0, "f");
  }, rh = function(t) {
    if (this.ended) return;
    let r = _(this, Vt, "m", Nk).call(this, t);
    switch (this._emit("streamEvent", t, r), t.type) {
      case "content_block_delta": {
        let o = r.content.at(-1);
        switch (t.delta.type) {
          case "text_delta": {
            if (o.type === "text") this._emit("text", t.delta.text, o.text || "");
            break;
          }
          case "citations_delta": {
            if (o.type === "text") this._emit("citation", t.delta.citation, o.citations ?? []);
            break;
          }
          case "input_json_delta": {
            if (Uk(o) && o.input) this._emit("inputJson", t.delta.partial_json, o.input);
            break;
          }
          case "thinking_delta": {
            if (o.type === "thinking") this._emit("thinking", t.delta.thinking, o.thinking);
            break;
          }
          case "signature_delta": {
            if (o.type === "thinking") this._emit("signature", o.signature);
            break;
          }
          case "compaction_delta": {
            if (o.type === "compaction" && o.content) this._emit("compaction", o.content);
            break;
          }
          default:
            zk(t.delta);
        }
        break;
      }
      case "message_stop": {
        this._addMessageParam(r), this._addMessage(Yg(r, _(this, li, "f"), { logger: _(this, ka, "f") }), true);
        break;
      }
      case "content_block_stop": {
        this._emit("contentBlock", r.content.at(-1));
        break;
      }
      case "message_start": {
        N(this, hn, r, "f");
        break;
      }
      case "content_block_start":
      case "message_delta":
        break;
    }
  }, nh = function() {
    if (this.ended) throw new z("stream has ended, this shouldn't happen");
    let t = _(this, hn, "f");
    if (!t) throw new z("request ended without sending any chunks");
    return N(this, hn, void 0, "f"), Yg(t, _(this, li, "f"), { logger: _(this, ka, "f") });
  }, Nk = function(t) {
    let r = _(this, hn, "f");
    if (t.type === "message_start") {
      if (r) throw new z(`Unexpected event order, got ${t.type} before receiving "message_stop"`);
      return t.message;
    }
    if (!r) throw new z(`Unexpected event order, got ${t.type} before "message_start"`);
    switch (t.type) {
      case "message_stop":
        return r;
      case "message_delta":
        if (r.container = t.delta.container, r.stop_reason = t.delta.stop_reason, r.stop_sequence = t.delta.stop_sequence, r.usage.output_tokens = t.usage.output_tokens, r.context_management = t.context_management, t.usage.input_tokens != null) r.usage.input_tokens = t.usage.input_tokens;
        if (t.usage.cache_creation_input_tokens != null) r.usage.cache_creation_input_tokens = t.usage.cache_creation_input_tokens;
        if (t.usage.cache_read_input_tokens != null) r.usage.cache_read_input_tokens = t.usage.cache_read_input_tokens;
        if (t.usage.server_tool_use != null) r.usage.server_tool_use = t.usage.server_tool_use;
        if (t.usage.iterations != null) r.usage.iterations = t.usage.iterations;
        return r;
      case "content_block_start":
        return r.content.push(t.content_block), r;
      case "content_block_delta": {
        let o = r.content.at(t.index);
        switch (t.delta.type) {
          case "text_delta": {
            if (o?.type === "text") r.content[t.index] = { ...o, text: (o.text || "") + t.delta.text };
            break;
          }
          case "citations_delta": {
            if (o?.type === "text") r.content[t.index] = { ...o, citations: [...o.citations ?? [], t.delta.citation] };
            break;
          }
          case "input_json_delta": {
            if (o && Uk(o)) {
              let n = o[jk] || "";
              n += t.delta.partial_json;
              let i = { ...o };
              if (Object.defineProperty(i, jk, { value: n, enumerable: false, writable: true }), n) try {
                i.input = Fu(n);
              } catch (s) {
                let a = new z(`Unable to parse tool parameter JSON from model. Please retry your request or adjust your prompt. Error: ${s}. JSON: ${n}`);
                _(this, Ku, "f").call(this, a);
              }
              r.content[t.index] = i;
            }
            break;
          }
          case "thinking_delta": {
            if (o?.type === "thinking") r.content[t.index] = { ...o, thinking: o.thinking + t.delta.thinking };
            break;
          }
          case "signature_delta": {
            if (o?.type === "thinking") r.content[t.index] = { ...o, signature: t.delta.signature };
            break;
          }
          case "compaction_delta": {
            if (o?.type === "compaction") r.content[t.index] = { ...o, content: (o.content || "") + t.delta.content };
            break;
          }
          default:
            zk(t.delta);
        }
        return r;
      }
      case "content_block_stop":
        return r;
    }
  }, Symbol.asyncIterator)]() {
    let e = [], t = [], r = false;
    return this.on("streamEvent", (o) => {
      let n = t.shift();
      if (n) n.resolve(o);
      else e.push(o);
    }), this.on("end", () => {
      r = true;
      for (let o of t) o.resolve(void 0);
      t.length = 0;
    }), this.on("abort", (o) => {
      r = true;
      for (let n of t) n.reject(o);
      t.length = 0;
    }), this.on("error", (o) => {
      r = true;
      for (let n of t) n.reject(o);
      t.length = 0;
    }), { next: async () => {
      if (!e.length) {
        if (r) return { value: void 0, done: true };
        return new Promise((n, i) => t.push({ resolve: n, reject: i })).then((n) => n ? { value: n, done: false } : { value: void 0, done: true });
      }
      return { value: e.shift(), done: false };
    }, return: async () => (this.abort(), { value: void 0, done: true }) };
  }
  toReadableStream() {
    return new Mt(this[Symbol.asyncIterator].bind(this), this.controller).toReadableStream();
  }
};
function zk(e) {
}
var ui = class extends Error {
  constructor(e) {
    let t = typeof e === "string" ? e : e.map((r) => {
      if (r.type === "text") return r.text;
      return `[${r.type}]`;
    }).join(" ");
    super(t);
    this.name = "ToolError", this.content = e;
  }
};
var Lk = 1e5;
var Fk = `You have been working on the task described above but have not yet completed it. Write a continuation summary that will allow you (or another instance of yourself) to resume work efficiently in a future context window where the conversation history will be replaced with this summary. Your summary should be structured, concise, and actionable. Include:
1. Task Overview
The user's core request and success criteria
Any clarifications or constraints they specified
2. Current State
What has been completed so far
Files created, modified, or analyzed (with paths if relevant)
Key outputs or artifacts produced
3. Important Discoveries
Technical constraints or requirements uncovered
Decisions made and their rationale
Errors encountered and how they were resolved
What approaches were tried that didn't work (and why)
4. Next Steps
Specific actions needed to complete the task
Any blockers or open questions to resolve
Priority order if multiple steps remain
5. Context to Preserve
User preferences or style requirements
Domain-specific details that aren't obvious
Any promises made to the user
Be concise but complete\u2014err on the side of including information that would prevent duplicate work or repeated mistakes. Write in a way that enables immediate resumption of the task.
Wrap your summary in <summary></summary> tags.`;
var Ta;
var di;
var ao;
var Ke;
var _t;
var Dt;
var jr;
var yn;
var Pa;
var Bk;
var oh;
function Hk() {
  let e, t;
  return { promise: new Promise((o, n) => {
    e = o, t = n;
  }), resolve: e, reject: t };
}
var Ia = class {
  constructor(e, t, r) {
    Ta.add(this), this.client = e, di.set(this, false), ao.set(this, false), Ke.set(this, void 0), _t.set(this, void 0), Dt.set(this, void 0), jr.set(this, void 0), yn.set(this, void 0), Pa.set(this, 0), N(this, Ke, { params: { ...t, messages: structuredClone(t.messages) } }, "f");
    let n = ["BetaToolRunner", ...Xg(t.tools, t.messages)].join(", ");
    if (N(this, _t, { ...r, headers: E([{ "x-stainless-helper": n }, r?.headers]) }, "f"), N(this, yn, Hk(), "f"), t.compactionControl?.enabled) console.warn('Anthropic: The `compactionControl` parameter is deprecated and will be removed in a future version. Use server-side compaction instead by passing `edits: [{ type: "compact_20260112" }]` in the params passed to `toolRunner()`. See https://platform.claude.com/docs/en/build-with-claude/compaction');
  }
  async *[(di = /* @__PURE__ */ new WeakMap(), ao = /* @__PURE__ */ new WeakMap(), Ke = /* @__PURE__ */ new WeakMap(), _t = /* @__PURE__ */ new WeakMap(), Dt = /* @__PURE__ */ new WeakMap(), jr = /* @__PURE__ */ new WeakMap(), yn = /* @__PURE__ */ new WeakMap(), Pa = /* @__PURE__ */ new WeakMap(), Ta = /* @__PURE__ */ new WeakSet(), Bk = async function() {
    let t = _(this, Ke, "f").params.compactionControl;
    if (!t || !t.enabled) return false;
    let r = 0;
    if (_(this, Dt, "f") !== void 0) try {
      let c = await _(this, Dt, "f");
      r = c.usage.input_tokens + (c.usage.cache_creation_input_tokens ?? 0) + (c.usage.cache_read_input_tokens ?? 0) + c.usage.output_tokens;
    } catch {
      return false;
    }
    let o = t.contextTokenThreshold ?? Lk;
    if (r < o) return false;
    let n = t.model ?? _(this, Ke, "f").params.model, i = t.summaryPrompt ?? Fk, s = _(this, Ke, "f").params.messages;
    if (s[s.length - 1].role === "assistant") {
      let c = s[s.length - 1];
      if (Array.isArray(c.content)) {
        let u = c.content.filter((d) => d.type !== "tool_use");
        if (u.length === 0) s.pop();
        else c.content = u;
      }
    }
    let a = await this.client.beta.messages.create({ model: n, messages: [...s, { role: "user", content: [{ type: "text", text: i }] }], max_tokens: _(this, Ke, "f").params.max_tokens }, { signal: _(this, _t, "f").signal, headers: E([_(this, _t, "f").headers, { "x-stainless-helper": "compaction" }]) });
    if (a.content[0]?.type !== "text") throw new z("Expected text response for compaction");
    return _(this, Ke, "f").params.messages = [{ role: "user", content: a.content }], true;
  }, Symbol.asyncIterator)]() {
    var e;
    if (_(this, di, "f")) throw new z("Cannot iterate over a consumed stream");
    N(this, di, true, "f"), N(this, ao, true, "f"), N(this, jr, void 0, "f");
    try {
      while (true) {
        let t;
        try {
          if (_(this, Ke, "f").params.max_iterations && _(this, Pa, "f") >= _(this, Ke, "f").params.max_iterations) break;
          N(this, ao, false, "f"), N(this, jr, void 0, "f"), N(this, Pa, (e = _(this, Pa, "f"), e++, e), "f"), N(this, Dt, void 0, "f");
          let { max_iterations: r, compactionControl: o, ...n } = _(this, Ke, "f").params;
          if (n.stream) t = this.client.beta.messages.stream({ ...n }, _(this, _t, "f")), N(this, Dt, t.finalMessage(), "f"), _(this, Dt, "f").catch(() => {
          }), yield t;
          else N(this, Dt, this.client.beta.messages.create({ ...n, stream: false }, _(this, _t, "f")), "f"), yield _(this, Dt, "f");
          if (!await _(this, Ta, "m", Bk).call(this)) {
            if (!_(this, ao, "f")) {
              let { role: a, content: c } = await _(this, Dt, "f");
              _(this, Ke, "f").params.messages.push({ role: a, content: c });
            }
            let s = await _(this, Ta, "m", oh).call(this, _(this, Ke, "f").params.messages.at(-1));
            if (s) _(this, Ke, "f").params.messages.push(s);
            else if (!_(this, ao, "f")) break;
          }
        } finally {
          if (t) t.abort();
        }
      }
      if (!_(this, Dt, "f")) throw new z("ToolRunner concluded without a message from the server");
      _(this, yn, "f").resolve(await _(this, Dt, "f"));
    } catch (t) {
      throw N(this, di, false, "f"), _(this, yn, "f").promise.catch(() => {
      }), _(this, yn, "f").reject(t), N(this, yn, Hk(), "f"), t;
    }
  }
  setMessagesParams(e) {
    if (typeof e === "function") _(this, Ke, "f").params = e(_(this, Ke, "f").params);
    else _(this, Ke, "f").params = e;
    N(this, ao, true, "f"), N(this, jr, void 0, "f");
  }
  setRequestOptions(e) {
    if (typeof e === "function") N(this, _t, e(_(this, _t, "f")), "f");
    else N(this, _t, { ..._(this, _t, "f"), ...e }, "f");
  }
  async generateToolResponse(e = _(this, _t, "f").signal) {
    let t = await _(this, Dt, "f") ?? this.params.messages.at(-1);
    if (!t) return null;
    return _(this, Ta, "m", oh).call(this, t, e);
  }
  done() {
    return _(this, yn, "f").promise;
  }
  async runUntilDone() {
    if (!_(this, di, "f")) for await (let e of this) ;
    return this.done();
  }
  get params() {
    return _(this, Ke, "f").params;
  }
  pushMessages(...e) {
    this.setMessagesParams((t) => ({ ...t, messages: [...t.messages, ...e] }));
  }
  then(e, t) {
    return this.runUntilDone().then(e, t);
  }
};
oh = async function(t, r = _(this, _t, "f").signal) {
  if (_(this, jr, "f") !== void 0) return _(this, jr, "f");
  return N(this, jr, K1(_(this, Ke, "f").params, t, { ..._(this, _t, "f"), signal: r }), "f"), _(this, jr, "f");
};
async function K1(e, t = e.messages.at(-1), r) {
  if (!t || t.role !== "assistant" || !t.content || typeof t.content === "string") return null;
  let o = t.content.filter((i) => i.type === "tool_use");
  if (o.length === 0) return null;
  return { role: "user", content: await Promise.all(o.map(async (i) => {
    let s = e.tools.find((a) => ("name" in a ? a.name : a.mcp_server_name) === i.name);
    if (!s || !("run" in s)) return { type: "tool_result", tool_use_id: i.id, content: `Error: Tool '${i.name}' not found`, is_error: true };
    try {
      let a = i.input;
      if ("parse" in s && s.parse) a = s.parse(a);
      let c = await s.run(a, { toolUseBlock: i, signal: r?.signal });
      return { type: "tool_result", tool_use_id: i.id, content: c };
    } catch (a) {
      return { type: "tool_result", tool_use_id: i.id, content: a instanceof ui ? a.content : `Error: ${a instanceof Error ? a.message : String(a)}`, is_error: true };
    }
  })) };
}
var qk = { "claude-1.3": "November 6th, 2024", "claude-1.3-100k": "November 6th, 2024", "claude-instant-1.1": "November 6th, 2024", "claude-instant-1.1-100k": "November 6th, 2024", "claude-instant-1.2": "November 6th, 2024", "claude-3-sonnet-20240229": "July 21st, 2025", "claude-3-opus-20240229": "January 5th, 2026", "claude-2.1": "July 21st, 2025", "claude-2.0": "July 21st, 2025", "claude-3-7-sonnet-latest": "February 19th, 2026", "claude-3-7-sonnet-20250219": "February 19th, 2026" };
var G1 = ["claude-mythos-preview", "claude-opus-4-6"];
var bn = class extends J {
  constructor() {
    super(...arguments);
    this.batches = new ba(this._client);
  }
  create(e, t) {
    let r = Zk(e), { betas: o, ...n } = r;
    if (n.model in qk) console.warn(`The model '${n.model}' is deprecated and will reach end-of-life on ${qk[n.model]}
Please migrate to a newer model. Visit https://docs.anthropic.com/en/docs/resources/model-deprecations for more information.`);
    if (G1.includes(n.model) && n.thinking && n.thinking.type === "enabled") console.warn(`Using Claude with ${n.model} and 'thinking.type=enabled' is deprecated. Use 'thinking.type=adaptive' instead which results in better model performance in our testing: https://platform.claude.com/docs/en/build-with-claude/adaptive-thinking`);
    let i = this._client._options.timeout;
    if (!n.stream && i == null) {
      let a = Lu[n.model] ?? void 0;
      i = this._client.calculateNonstreamingTimeout(n.max_tokens, a);
    }
    let s = zu(n.tools, n.messages);
    return this._client.post("/v1/messages?beta=true", { body: n, timeout: i ?? 6e5, ...t, headers: E([{ ...o?.toString() != null ? { "anthropic-beta": o?.toString() } : void 0 }, s, t?.headers]), stream: r.stream ?? false });
  }
  parse(e, t) {
    return t = { ...t, headers: E([{ "anthropic-beta": [...e.betas ?? [], "structured-outputs-2025-12-15"].toString() }, t?.headers]) }, this.create(e, t).then((r) => Qg(r, e, { logger: this._client.logger ?? console }));
  }
  stream(e, t) {
    return Ea.createMessage(this, e, t);
  }
  countTokens(e, t) {
    let r = Zk(e), { betas: o, ...n } = r;
    return this._client.post("/v1/messages/count_tokens?beta=true", { body: n, ...t, headers: E([{ "anthropic-beta": [...o ?? [], "token-counting-2024-11-01"].toString() }, t?.headers]) });
  }
  toolRunner(e, t) {
    return new Ia(this._client, e, t);
  }
};
function Zk(e) {
  if (!e.output_format) return e;
  if (e.output_config?.format) throw new z("Both output_format and output_config.format were provided. Please use only output_config.format (output_format is deprecated).");
  let { output_format: t, ...r } = e;
  return { ...r, output_config: { ...e.output_config, format: t } };
}
bn.Batches = ba;
bn.BetaToolRunner = Ia;
bn.ToolError = ui;
var Ra = class extends J {
  list(e, t = {}, r) {
    let { betas: o, ...n } = t ?? {};
    return this._client.getAPIList(M`/v1/sessions/${e}/events?beta=true`, ye, { query: n, ...r, headers: E([{ "anthropic-beta": [...o ?? [], "managed-agents-2026-04-01"].toString() }, r?.headers]) });
  }
  send(e, t, r) {
    let { betas: o, ...n } = t;
    return this._client.post(M`/v1/sessions/${e}/events?beta=true`, { body: n, ...r, headers: E([{ "anthropic-beta": [...o ?? [], "managed-agents-2026-04-01"].toString() }, r?.headers]) });
  }
  stream(e, t = {}, r) {
    let { betas: o } = t ?? {};
    return this._client.get(M`/v1/sessions/${e}/events/stream?beta=true`, { ...r, headers: E([{ "anthropic-beta": [...o ?? [], "managed-agents-2026-04-01"].toString() }, r?.headers]), stream: true });
  }
};
var $a = class extends J {
  retrieve(e, t, r) {
    let { session_id: o, betas: n } = t;
    return this._client.get(M`/v1/sessions/${o}/resources/${e}?beta=true`, { ...r, headers: E([{ "anthropic-beta": [...n ?? [], "managed-agents-2026-04-01"].toString() }, r?.headers]) });
  }
  update(e, t, r) {
    let { session_id: o, betas: n, ...i } = t;
    return this._client.post(M`/v1/sessions/${o}/resources/${e}?beta=true`, { body: i, ...r, headers: E([{ "anthropic-beta": [...n ?? [], "managed-agents-2026-04-01"].toString() }, r?.headers]) });
  }
  list(e, t = {}, r) {
    let { betas: o, ...n } = t ?? {};
    return this._client.getAPIList(M`/v1/sessions/${e}/resources?beta=true`, ye, { query: n, ...r, headers: E([{ "anthropic-beta": [...o ?? [], "managed-agents-2026-04-01"].toString() }, r?.headers]) });
  }
  delete(e, t, r) {
    let { session_id: o, betas: n } = t;
    return this._client.delete(M`/v1/sessions/${o}/resources/${e}?beta=true`, { ...r, headers: E([{ "anthropic-beta": [...n ?? [], "managed-agents-2026-04-01"].toString() }, r?.headers]) });
  }
  add(e, t, r) {
    let { betas: o, ...n } = t;
    return this._client.post(M`/v1/sessions/${e}/resources?beta=true`, { body: n, ...r, headers: E([{ "anthropic-beta": [...o ?? [], "managed-agents-2026-04-01"].toString() }, r?.headers]) });
  }
};
var co = class extends J {
  constructor() {
    super(...arguments);
    this.events = new Ra(this._client), this.resources = new $a(this._client);
  }
  create(e, t) {
    let { betas: r, ...o } = e;
    return this._client.post("/v1/sessions?beta=true", { body: o, ...t, headers: E([{ "anthropic-beta": [...r ?? [], "managed-agents-2026-04-01"].toString() }, t?.headers]) });
  }
  retrieve(e, t = {}, r) {
    let { betas: o } = t ?? {};
    return this._client.get(M`/v1/sessions/${e}?beta=true`, { ...r, headers: E([{ "anthropic-beta": [...o ?? [], "managed-agents-2026-04-01"].toString() }, r?.headers]) });
  }
  update(e, t, r) {
    let { betas: o, ...n } = t;
    return this._client.post(M`/v1/sessions/${e}?beta=true`, { body: n, ...r, headers: E([{ "anthropic-beta": [...o ?? [], "managed-agents-2026-04-01"].toString() }, r?.headers]) });
  }
  list(e = {}, t) {
    let { betas: r, ...o } = e ?? {};
    return this._client.getAPIList("/v1/sessions?beta=true", ye, { query: o, ...t, headers: E([{ "anthropic-beta": [...r ?? [], "managed-agents-2026-04-01"].toString() }, t?.headers]) });
  }
  delete(e, t = {}, r) {
    let { betas: o } = t ?? {};
    return this._client.delete(M`/v1/sessions/${e}?beta=true`, { ...r, headers: E([{ "anthropic-beta": [...o ?? [], "managed-agents-2026-04-01"].toString() }, r?.headers]) });
  }
  archive(e, t = {}, r) {
    let { betas: o } = t ?? {};
    return this._client.post(M`/v1/sessions/${e}/archive?beta=true`, { ...r, headers: E([{ "anthropic-beta": [...o ?? [], "managed-agents-2026-04-01"].toString() }, r?.headers]) });
  }
};
co.Events = Ra;
co.Resources = $a;
var Oa = class extends J {
  create(e, t = {}, r) {
    let { betas: o, ...n } = t ?? {};
    return this._client.post(M`/v1/skills/${e}/versions?beta=true`, ii({ body: n, ...r, headers: E([{ "anthropic-beta": [...o ?? [], "skills-2025-10-02"].toString() }, r?.headers]) }, this._client));
  }
  retrieve(e, t, r) {
    let { skill_id: o, betas: n } = t;
    return this._client.get(M`/v1/skills/${o}/versions/${e}?beta=true`, { ...r, headers: E([{ "anthropic-beta": [...n ?? [], "skills-2025-10-02"].toString() }, r?.headers]) });
  }
  list(e, t = {}, r) {
    let { betas: o, ...n } = t ?? {};
    return this._client.getAPIList(M`/v1/skills/${e}/versions?beta=true`, ye, { query: n, ...r, headers: E([{ "anthropic-beta": [...o ?? [], "skills-2025-10-02"].toString() }, r?.headers]) });
  }
  delete(e, t, r) {
    let { skill_id: o, betas: n } = t;
    return this._client.delete(M`/v1/skills/${o}/versions/${e}?beta=true`, { ...r, headers: E([{ "anthropic-beta": [...n ?? [], "skills-2025-10-02"].toString() }, r?.headers]) });
  }
};
var pi = class extends J {
  constructor() {
    super(...arguments);
    this.versions = new Oa(this._client);
  }
  create(e = {}, t) {
    let { betas: r, ...o } = e ?? {};
    return this._client.post("/v1/skills?beta=true", ii({ body: o, ...t, headers: E([{ "anthropic-beta": [...r ?? [], "skills-2025-10-02"].toString() }, t?.headers]) }, this._client, false));
  }
  retrieve(e, t = {}, r) {
    let { betas: o } = t ?? {};
    return this._client.get(M`/v1/skills/${e}?beta=true`, { ...r, headers: E([{ "anthropic-beta": [...o ?? [], "skills-2025-10-02"].toString() }, r?.headers]) });
  }
  list(e = {}, t) {
    let { betas: r, ...o } = e ?? {};
    return this._client.getAPIList("/v1/skills?beta=true", ye, { query: o, ...t, headers: E([{ "anthropic-beta": [...r ?? [], "skills-2025-10-02"].toString() }, t?.headers]) });
  }
  delete(e, t = {}, r) {
    let { betas: o } = t ?? {};
    return this._client.delete(M`/v1/skills/${e}?beta=true`, { ...r, headers: E([{ "anthropic-beta": [...o ?? [], "skills-2025-10-02"].toString() }, r?.headers]) });
  }
};
pi.Versions = Oa;
var Aa = class extends J {
  create(e, t, r) {
    let { betas: o, ...n } = t;
    return this._client.post(M`/v1/vaults/${e}/credentials?beta=true`, { body: n, ...r, headers: E([{ "anthropic-beta": [...o ?? [], "managed-agents-2026-04-01"].toString() }, r?.headers]) });
  }
  retrieve(e, t, r) {
    let { vault_id: o, betas: n } = t;
    return this._client.get(M`/v1/vaults/${o}/credentials/${e}?beta=true`, { ...r, headers: E([{ "anthropic-beta": [...n ?? [], "managed-agents-2026-04-01"].toString() }, r?.headers]) });
  }
  update(e, t, r) {
    let { vault_id: o, betas: n, ...i } = t;
    return this._client.post(M`/v1/vaults/${o}/credentials/${e}?beta=true`, { body: i, ...r, headers: E([{ "anthropic-beta": [...n ?? [], "managed-agents-2026-04-01"].toString() }, r?.headers]) });
  }
  list(e, t = {}, r) {
    let { betas: o, ...n } = t ?? {};
    return this._client.getAPIList(M`/v1/vaults/${e}/credentials?beta=true`, ye, { query: n, ...r, headers: E([{ "anthropic-beta": [...o ?? [], "managed-agents-2026-04-01"].toString() }, r?.headers]) });
  }
  delete(e, t, r) {
    let { vault_id: o, betas: n } = t;
    return this._client.delete(M`/v1/vaults/${o}/credentials/${e}?beta=true`, { ...r, headers: E([{ "anthropic-beta": [...n ?? [], "managed-agents-2026-04-01"].toString() }, r?.headers]) });
  }
  archive(e, t, r) {
    let { vault_id: o, betas: n } = t;
    return this._client.post(M`/v1/vaults/${o}/credentials/${e}/archive?beta=true`, { ...r, headers: E([{ "anthropic-beta": [...n ?? [], "managed-agents-2026-04-01"].toString() }, r?.headers]) });
  }
};
var fi = class extends J {
  constructor() {
    super(...arguments);
    this.credentials = new Aa(this._client);
  }
  create(e, t) {
    let { betas: r, ...o } = e;
    return this._client.post("/v1/vaults?beta=true", { body: o, ...t, headers: E([{ "anthropic-beta": [...r ?? [], "managed-agents-2026-04-01"].toString() }, t?.headers]) });
  }
  retrieve(e, t = {}, r) {
    let { betas: o } = t ?? {};
    return this._client.get(M`/v1/vaults/${e}?beta=true`, { ...r, headers: E([{ "anthropic-beta": [...o ?? [], "managed-agents-2026-04-01"].toString() }, r?.headers]) });
  }
  update(e, t, r) {
    let { betas: o, ...n } = t;
    return this._client.post(M`/v1/vaults/${e}?beta=true`, { body: n, ...r, headers: E([{ "anthropic-beta": [...o ?? [], "managed-agents-2026-04-01"].toString() }, r?.headers]) });
  }
  list(e = {}, t) {
    let { betas: r, ...o } = e ?? {};
    return this._client.getAPIList("/v1/vaults?beta=true", ye, { query: o, ...t, headers: E([{ "anthropic-beta": [...r ?? [], "managed-agents-2026-04-01"].toString() }, t?.headers]) });
  }
  delete(e, t = {}, r) {
    let { betas: o } = t ?? {};
    return this._client.delete(M`/v1/vaults/${e}?beta=true`, { ...r, headers: E([{ "anthropic-beta": [...o ?? [], "managed-agents-2026-04-01"].toString() }, r?.headers]) });
  }
  archive(e, t = {}, r) {
    let { betas: o } = t ?? {};
    return this._client.post(M`/v1/vaults/${e}/archive?beta=true`, { ...r, headers: E([{ "anthropic-beta": [...o ?? [], "managed-agents-2026-04-01"].toString() }, r?.headers]) });
  }
};
fi.Credentials = Aa;
var it = class extends J {
  constructor() {
    super(...arguments);
    this.models = new fa(this._client), this.messages = new bn(this._client), this.agents = new si(this._client), this.environments = new ua(this._client), this.sessions = new co(this._client), this.vaults = new fi(this._client), this.memoryStores = new io(this._client), this.files = new pa(this._client), this.skills = new pi(this._client), this.userProfiles = new ma(this._client);
  }
};
it.Models = fa;
it.Messages = bn;
it.Agents = si;
it.Environments = ua;
it.Sessions = co;
it.Vaults = fi;
it.MemoryStores = io;
it.Files = pa;
it.Skills = pi;
it.UserProfiles = ma;
var mi = class extends J {
  create(e, t) {
    let { betas: r, ...o } = e;
    return this._client.post("/v1/complete", { body: o, timeout: this._client._options.timeout ?? 6e5, ...t, headers: E([{ ...r?.toString() != null ? { "anthropic-beta": r?.toString() } : void 0 }, t?.headers]), stream: e.stream ?? false });
  }
};
function Vk(e) {
  return e?.output_config?.format;
}
function ih(e, t, r) {
  let o = Vk(t);
  if (!t || !("parse" in (o ?? {}))) return { ...e, content: e.content.map((n) => {
    if (n.type === "text") return Object.defineProperty({ ...n }, "parsed_output", { value: null, enumerable: false });
    return n;
  }), parsed_output: null };
  return sh(e, t, r);
}
function sh(e, t, r) {
  let o = null, n = e.content.map((i) => {
    if (i.type === "text") {
      let s = oF(t, i.text);
      if (o === null) o = s;
      return Object.defineProperty({ ...i }, "parsed_output", { value: s, enumerable: false });
    }
    return i;
  });
  return { ...e, content: n, parsed_output: o };
}
function oF(e, t) {
  let r = Vk(e);
  if (r?.type !== "json_schema") return null;
  try {
    if ("parse" in r) return r.parse(t);
    return JSON.parse(t);
  } catch (o) {
    throw new z(`Failed to parse structured output: ${o}`);
  }
}
var Wt;
var _n;
var gi;
var Ca;
var Gu;
var Ma;
var Da;
var Ju;
var Na;
var Ur;
var ja;
var Xu;
var Yu;
var lo;
var Qu;
var ed;
var Ua;
var ah;
var Wk;
var ch;
var lh;
var uh;
var dh;
var Kk;
var Gk = "__json_buf";
function Jk(e) {
  return e.type === "tool_use" || e.type === "server_tool_use";
}
var za = class _za {
  constructor(e, t) {
    Wt.add(this), this.messages = [], this.receivedMessages = [], _n.set(this, void 0), gi.set(this, null), this.controller = new AbortController(), Ca.set(this, void 0), Gu.set(this, () => {
    }), Ma.set(this, () => {
    }), Da.set(this, void 0), Ju.set(this, () => {
    }), Na.set(this, () => {
    }), Ur.set(this, {}), ja.set(this, false), Xu.set(this, false), Yu.set(this, false), lo.set(this, false), Qu.set(this, void 0), ed.set(this, void 0), Ua.set(this, void 0), ch.set(this, (r) => {
      if (N(this, Xu, true, "f"), Mr(r)) r = new et();
      if (r instanceof et) return N(this, Yu, true, "f"), this._emit("abort", r);
      if (r instanceof z) return this._emit("error", r);
      if (r instanceof Error) {
        let o = new z(r.message);
        return o.cause = r, this._emit("error", o);
      }
      return this._emit("error", new z(String(r)));
    }), N(this, Ca, new Promise((r, o) => {
      N(this, Gu, r, "f"), N(this, Ma, o, "f");
    }), "f"), N(this, Da, new Promise((r, o) => {
      N(this, Ju, r, "f"), N(this, Na, o, "f");
    }), "f"), _(this, Ca, "f").catch(() => {
    }), _(this, Da, "f").catch(() => {
    }), N(this, gi, e, "f"), N(this, Ua, t?.logger ?? console, "f");
  }
  get response() {
    return _(this, Qu, "f");
  }
  get request_id() {
    return _(this, ed, "f");
  }
  async withResponse() {
    N(this, lo, true, "f");
    let e = await _(this, Ca, "f");
    if (!e) throw Error("Could not resolve a `Response` object");
    return { data: this, response: e, request_id: e.headers.get("request-id") };
  }
  static fromReadableStream(e) {
    let t = new _za(null);
    return t._run(() => t._fromReadableStream(e)), t;
  }
  static createMessage(e, t, r, { logger: o } = {}) {
    let n = new _za(t, { logger: o });
    for (let i of t.messages) n._addMessageParam(i);
    return N(n, gi, { ...t, stream: true }, "f"), n._run(() => n._createMessage(e, { ...t, stream: true }, { ...r, headers: { ...r?.headers, "X-Stainless-Helper-Method": "stream" } })), n;
  }
  _run(e) {
    e().then(() => {
      this._emitFinal(), this._emit("end");
    }, _(this, ch, "f"));
  }
  _addMessageParam(e) {
    this.messages.push(e);
  }
  _addMessage(e, t = true) {
    if (this.receivedMessages.push(e), t) this._emit("message", e);
  }
  async _createMessage(e, t, r) {
    let o = r?.signal, n;
    if (o) {
      if (o.aborted) this.controller.abort();
      n = this.controller.abort.bind(this.controller), o.addEventListener("abort", n);
    }
    try {
      _(this, Wt, "m", lh).call(this);
      let { response: i, data: s } = await e.create({ ...t, stream: true }, { ...r, signal: this.controller.signal }).withResponse();
      this._connected(i);
      for await (let a of s) _(this, Wt, "m", uh).call(this, a);
      if (s.controller.signal?.aborted) throw new et();
      _(this, Wt, "m", dh).call(this);
    } finally {
      if (o && n) o.removeEventListener("abort", n);
    }
  }
  _connected(e) {
    if (this.ended) return;
    N(this, Qu, e, "f"), N(this, ed, e?.headers.get("request-id"), "f"), _(this, Gu, "f").call(this, e), this._emit("connect");
  }
  get ended() {
    return _(this, ja, "f");
  }
  get errored() {
    return _(this, Xu, "f");
  }
  get aborted() {
    return _(this, Yu, "f");
  }
  abort() {
    this.controller.abort();
  }
  on(e, t) {
    return (_(this, Ur, "f")[e] || (_(this, Ur, "f")[e] = [])).push({ listener: t }), this;
  }
  off(e, t) {
    let r = _(this, Ur, "f")[e];
    if (!r) return this;
    let o = r.findIndex((n) => n.listener === t);
    if (o >= 0) r.splice(o, 1);
    return this;
  }
  once(e, t) {
    return (_(this, Ur, "f")[e] || (_(this, Ur, "f")[e] = [])).push({ listener: t, once: true }), this;
  }
  emitted(e) {
    return new Promise((t, r) => {
      if (N(this, lo, true, "f"), e !== "error") this.once("error", r);
      this.once(e, t);
    });
  }
  async done() {
    N(this, lo, true, "f"), await _(this, Da, "f");
  }
  get currentMessage() {
    return _(this, _n, "f");
  }
  async finalMessage() {
    return await this.done(), _(this, Wt, "m", ah).call(this);
  }
  async finalText() {
    return await this.done(), _(this, Wt, "m", Wk).call(this);
  }
  _emit(e, ...t) {
    if (_(this, ja, "f")) return;
    if (e === "end") N(this, ja, true, "f"), _(this, Ju, "f").call(this);
    let r = _(this, Ur, "f")[e];
    if (r) _(this, Ur, "f")[e] = r.filter((o) => !o.once), r.forEach(({ listener: o }) => o(...t));
    if (e === "abort") {
      let o = t[0];
      if (!_(this, lo, "f") && !r?.length) Promise.reject(o);
      _(this, Ma, "f").call(this, o), _(this, Na, "f").call(this, o), this._emit("end");
      return;
    }
    if (e === "error") {
      let o = t[0];
      if (!_(this, lo, "f") && !r?.length) Promise.reject(o);
      _(this, Ma, "f").call(this, o), _(this, Na, "f").call(this, o), this._emit("end");
    }
  }
  _emitFinal() {
    if (this.receivedMessages.at(-1)) this._emit("finalMessage", _(this, Wt, "m", ah).call(this));
  }
  async _fromReadableStream(e, t) {
    let r = t?.signal, o;
    if (r) {
      if (r.aborted) this.controller.abort();
      o = this.controller.abort.bind(this.controller), r.addEventListener("abort", o);
    }
    try {
      _(this, Wt, "m", lh).call(this), this._connected(null);
      let n = Mt.fromReadableStream(e, this.controller);
      for await (let i of n) _(this, Wt, "m", uh).call(this, i);
      if (n.controller.signal?.aborted) throw new et();
      _(this, Wt, "m", dh).call(this);
    } finally {
      if (r && o) r.removeEventListener("abort", o);
    }
  }
  [(_n = /* @__PURE__ */ new WeakMap(), gi = /* @__PURE__ */ new WeakMap(), Ca = /* @__PURE__ */ new WeakMap(), Gu = /* @__PURE__ */ new WeakMap(), Ma = /* @__PURE__ */ new WeakMap(), Da = /* @__PURE__ */ new WeakMap(), Ju = /* @__PURE__ */ new WeakMap(), Na = /* @__PURE__ */ new WeakMap(), Ur = /* @__PURE__ */ new WeakMap(), ja = /* @__PURE__ */ new WeakMap(), Xu = /* @__PURE__ */ new WeakMap(), Yu = /* @__PURE__ */ new WeakMap(), lo = /* @__PURE__ */ new WeakMap(), Qu = /* @__PURE__ */ new WeakMap(), ed = /* @__PURE__ */ new WeakMap(), Ua = /* @__PURE__ */ new WeakMap(), ch = /* @__PURE__ */ new WeakMap(), Wt = /* @__PURE__ */ new WeakSet(), ah = function() {
    if (this.receivedMessages.length === 0) throw new z("stream ended without producing a Message with role=assistant");
    return this.receivedMessages.at(-1);
  }, Wk = function() {
    if (this.receivedMessages.length === 0) throw new z("stream ended without producing a Message with role=assistant");
    let t = this.receivedMessages.at(-1).content.filter((r) => r.type === "text").map((r) => r.text);
    if (t.length === 0) throw new z("stream ended without producing a content block with type=text");
    return t.join(" ");
  }, lh = function() {
    if (this.ended) return;
    N(this, _n, void 0, "f");
  }, uh = function(t) {
    if (this.ended) return;
    let r = _(this, Wt, "m", Kk).call(this, t);
    switch (this._emit("streamEvent", t, r), t.type) {
      case "content_block_delta": {
        let o = r.content.at(-1);
        switch (t.delta.type) {
          case "text_delta": {
            if (o.type === "text") this._emit("text", t.delta.text, o.text || "");
            break;
          }
          case "citations_delta": {
            if (o.type === "text") this._emit("citation", t.delta.citation, o.citations ?? []);
            break;
          }
          case "input_json_delta": {
            if (Jk(o) && o.input) this._emit("inputJson", t.delta.partial_json, o.input);
            break;
          }
          case "thinking_delta": {
            if (o.type === "thinking") this._emit("thinking", t.delta.thinking, o.thinking);
            break;
          }
          case "signature_delta": {
            if (o.type === "thinking") this._emit("signature", o.signature);
            break;
          }
          default:
            Xk(t.delta);
        }
        break;
      }
      case "message_stop": {
        this._addMessageParam(r), this._addMessage(ih(r, _(this, gi, "f"), { logger: _(this, Ua, "f") }), true);
        break;
      }
      case "content_block_stop": {
        this._emit("contentBlock", r.content.at(-1));
        break;
      }
      case "message_start": {
        N(this, _n, r, "f");
        break;
      }
      case "content_block_start":
      case "message_delta":
        break;
    }
  }, dh = function() {
    if (this.ended) throw new z("stream has ended, this shouldn't happen");
    let t = _(this, _n, "f");
    if (!t) throw new z("request ended without sending any chunks");
    return N(this, _n, void 0, "f"), ih(t, _(this, gi, "f"), { logger: _(this, Ua, "f") });
  }, Kk = function(t) {
    let r = _(this, _n, "f");
    if (t.type === "message_start") {
      if (r) throw new z(`Unexpected event order, got ${t.type} before receiving "message_stop"`);
      return t.message;
    }
    if (!r) throw new z(`Unexpected event order, got ${t.type} before "message_start"`);
    switch (t.type) {
      case "message_stop":
        return r;
      case "message_delta":
        if (r.stop_reason = t.delta.stop_reason, r.stop_sequence = t.delta.stop_sequence, r.usage.output_tokens = t.usage.output_tokens, t.usage.input_tokens != null) r.usage.input_tokens = t.usage.input_tokens;
        if (t.usage.cache_creation_input_tokens != null) r.usage.cache_creation_input_tokens = t.usage.cache_creation_input_tokens;
        if (t.usage.cache_read_input_tokens != null) r.usage.cache_read_input_tokens = t.usage.cache_read_input_tokens;
        if (t.usage.server_tool_use != null) r.usage.server_tool_use = t.usage.server_tool_use;
        return r;
      case "content_block_start":
        return r.content.push({ ...t.content_block }), r;
      case "content_block_delta": {
        let o = r.content.at(t.index);
        switch (t.delta.type) {
          case "text_delta": {
            if (o?.type === "text") r.content[t.index] = { ...o, text: (o.text || "") + t.delta.text };
            break;
          }
          case "citations_delta": {
            if (o?.type === "text") r.content[t.index] = { ...o, citations: [...o.citations ?? [], t.delta.citation] };
            break;
          }
          case "input_json_delta": {
            if (o && Jk(o)) {
              let n = o[Gk] || "";
              n += t.delta.partial_json;
              let i = { ...o };
              if (Object.defineProperty(i, Gk, { value: n, enumerable: false, writable: true }), n) i.input = Fu(n);
              r.content[t.index] = i;
            }
            break;
          }
          case "thinking_delta": {
            if (o?.type === "thinking") r.content[t.index] = { ...o, thinking: o.thinking + t.delta.thinking };
            break;
          }
          case "signature_delta": {
            if (o?.type === "thinking") r.content[t.index] = { ...o, signature: t.delta.signature };
            break;
          }
          default:
            Xk(t.delta);
        }
        return r;
      }
      case "content_block_stop":
        return r;
    }
  }, Symbol.asyncIterator)]() {
    let e = [], t = [], r = false;
    return this.on("streamEvent", (o) => {
      let n = t.shift();
      if (n) n.resolve(o);
      else e.push(o);
    }), this.on("end", () => {
      r = true;
      for (let o of t) o.resolve(void 0);
      t.length = 0;
    }), this.on("abort", (o) => {
      r = true;
      for (let n of t) n.reject(o);
      t.length = 0;
    }), this.on("error", (o) => {
      r = true;
      for (let n of t) n.reject(o);
      t.length = 0;
    }), { next: async () => {
      if (!e.length) {
        if (r) return { value: void 0, done: true };
        return new Promise((n, i) => t.push({ resolve: n, reject: i })).then((n) => n ? { value: n, done: false } : { value: void 0, done: true });
      }
      return { value: e.shift(), done: false };
    }, return: async () => (this.abort(), { value: void 0, done: true }) };
  }
  toReadableStream() {
    return new Mt(this[Symbol.asyncIterator].bind(this), this.controller).toReadableStream();
  }
};
function Xk(e) {
}
var La = class extends J {
  create(e, t) {
    return this._client.post("/v1/messages/batches", { body: e, ...t });
  }
  retrieve(e, t) {
    return this._client.get(M`/v1/messages/batches/${e}`, t);
  }
  list(e = {}, t) {
    return this._client.getAPIList("/v1/messages/batches", ir, { query: e, ...t });
  }
  delete(e, t) {
    return this._client.delete(M`/v1/messages/batches/${e}`, t);
  }
  cancel(e, t) {
    return this._client.post(M`/v1/messages/batches/${e}/cancel`, t);
  }
  async results(e, t) {
    let r = await this.retrieve(e);
    if (!r.results_url) throw new z(`No batch \`results_url\`; Has it finished processing? ${r.processing_status} - ${r.id}`);
    return this._client.get(r.results_url, { ...t, headers: E([{ Accept: "application/binary" }, t?.headers]), stream: true, __binaryResponse: true })._thenUnwrap((o, n) => ai.fromResponse(n.response, n.controller));
  }
};
var uo = class extends J {
  constructor() {
    super(...arguments);
    this.batches = new La(this._client);
  }
  create(e, t) {
    if (e.model in Yk) console.warn(`The model '${e.model}' is deprecated and will reach end-of-life on ${Yk[e.model]}
Please migrate to a newer model. Visit https://docs.anthropic.com/en/docs/resources/model-deprecations for more information.`);
    if (sF.includes(e.model) && e.thinking && e.thinking.type === "enabled") console.warn(`Using Claude with ${e.model} and 'thinking.type=enabled' is deprecated. Use 'thinking.type=adaptive' instead which results in better model performance in our testing: https://platform.claude.com/docs/en/build-with-claude/adaptive-thinking`);
    let r = this._client._options.timeout;
    if (!e.stream && r == null) {
      let n = Lu[e.model] ?? void 0;
      r = this._client.calculateNonstreamingTimeout(e.max_tokens, n);
    }
    let o = zu(e.tools, e.messages);
    return this._client.post("/v1/messages", { body: e, timeout: r ?? 6e5, ...t, headers: E([o, t?.headers]), stream: e.stream ?? false });
  }
  parse(e, t) {
    return this.create(e, t).then((r) => sh(r, e, { logger: this._client.logger ?? console }));
  }
  stream(e, t) {
    return za.createMessage(this, e, t, { logger: this._client.logger ?? console });
  }
  countTokens(e, t) {
    return this._client.post("/v1/messages/count_tokens", { body: e, ...t });
  }
};
var Yk = { "claude-1.3": "November 6th, 2024", "claude-1.3-100k": "November 6th, 2024", "claude-instant-1.1": "November 6th, 2024", "claude-instant-1.1-100k": "November 6th, 2024", "claude-instant-1.2": "November 6th, 2024", "claude-3-sonnet-20240229": "July 21st, 2025", "claude-3-opus-20240229": "January 5th, 2026", "claude-2.1": "July 21st, 2025", "claude-2.0": "July 21st, 2025", "claude-3-7-sonnet-latest": "February 19th, 2026", "claude-3-7-sonnet-20250219": "February 19th, 2026", "claude-3-5-haiku-latest": "February 19th, 2026", "claude-3-5-haiku-20241022": "February 19th, 2026", "claude-opus-4-0": "June 15th, 2026", "claude-opus-4-20250514": "June 15th, 2026", "claude-sonnet-4-0": "June 15th, 2026", "claude-sonnet-4-20250514": "June 15th, 2026" };
var sF = ["claude-mythos-preview", "claude-opus-4-6"];
uo.Batches = La;
var hi = class extends J {
  retrieve(e, t = {}, r) {
    let { betas: o } = t ?? {};
    return this._client.get(M`/v1/models/${e}`, { ...r, headers: E([{ ...o?.toString() != null ? { "anthropic-beta": o?.toString() } : void 0 }, r?.headers]) });
  }
  list(e = {}, t) {
    let { betas: r, ...o } = e ?? {};
    return this._client.getAPIList("/v1/models", ir, { query: o, ...t, headers: E([{ ...r?.toString() != null ? { "anthropic-beta": r?.toString() } : void 0 }, t?.headers]) });
  }
};
var ph;
var fh;
var td;
var Qk;
var eE = "\\n\\nHuman:";
var tE = "\\n\\nAssistant:";
var Ue = class {
  get credentials() {
    return this._authState.provider;
  }
  constructor({ baseURL: e = ge("ANTHROPIC_BASE_URL"), apiKey: t, authToken: r, ...o } = {}) {
    if (ph.add(this), this._requestAuthFlags = /* @__PURE__ */ new WeakMap(), td.set(this, void 0), t === void 0) t = o.profile != null ? null : ge("ANTHROPIC_API_KEY") ?? null;
    if (r === void 0) r = o.profile != null ? null : ge("ANTHROPIC_AUTH_TOKEN") ?? null;
    if (o.profile != null && (o.credentials != null || o.config != null)) throw TypeError("Pass at most one of `profile`, `credentials`, or `config`.");
    let n = { apiKey: t, authToken: r, ...o, baseURL: e || "https://api.anthropic.com" };
    if (!n.dangerouslyAllowBrowser && nk()) throw new z(`It looks like you're running in a browser-like environment.

This is disabled by default, as it risks exposing your secret API credentials to attackers.
If you understand the risks and have appropriate mitigations in place,
you can set the \`dangerouslyAllowBrowser\` option to \`true\`, e.g.,

new Anthropic({ apiKey, dangerouslyAllowBrowser: true });
`);
    this.baseURL = n.baseURL, this._baseURLIsExplicit = o.__baseURLIsExplicit ?? !!e, this.timeout = n.timeout ?? fh.DEFAULT_TIMEOUT, this.logger = n.logger ?? console;
    let i = "warn";
    this.logLevel = i, this.logLevel = Fg(n.logLevel, "ClientOptions.logLevel", this) ?? Fg(ge("ANTHROPIC_LOG"), "process.env['ANTHROPIC_LOG']", this) ?? i, this.fetchOptions = n.fetchOptions, this.maxRetries = n.maxRetries ?? 2, this.fetch = n.fetch ?? ok(), N(this, td, sk, "f");
    let s = ge("ANTHROPIC_CUSTOM_HEADERS");
    if (s) {
      let c = {};
      for (let u of s.split(`
`)) {
        let d = u.indexOf(":");
        if (d >= 0) c[u.substring(0, d).trim()] = u.substring(d + 1).trim();
      }
      n.defaultHeaders = { ...c, ...n.defaultHeaders };
    }
    let a = o.__auth;
    if (delete n.__auth, delete n.__baseURLIsExplicit, this._options = n, this.apiKey = typeof t === "string" ? t : null, this.authToken = r, a) {
      if (this._authState = a, !this._baseURLIsExplicit && a.baseURL) this.baseURL = a.baseURL;
    } else if (this._authState = { provider: null, tokenCache: null, resolution: null, error: null, extraHeaders: {} }, this.apiKey == null && this.authToken == null) {
      let c = n.credentials ?? null;
      if (c) this._authState.provider = c, this._authState.tokenCache = this._makeTokenCache(c);
      else if (n.config != null) {
        let u = qg(n.config, this._credentialResolverOptions());
        this._authState.provider = u.provider, this._authState.tokenCache = this._makeTokenCache(u.provider), this._authState.extraHeaders = u.extraHeaders, this._applyCredentialBaseURL(u.baseURL);
      } else if (n.profile != null) this._authState.resolution = this._resolveDefaultCredentials(n.profile);
      else this._authState.resolution = this._resolveDefaultCredentials();
    }
  }
  _applyCredentialBaseURL(e) {
    if (!e) return;
    let t = e.replace(/\/+$/, "");
    if (this._authState.baseURL = t, !this._baseURLIsExplicit) this.baseURL = t;
  }
  _credentialResolverOptions() {
    return { baseURL: this.baseURL, fetch: this.fetch, userAgent: this.getUserAgent(), onCacheWriteError: (e) => {
      Fe(this).debug("credential cache write failed (best-effort)", e);
    }, onSafetyWarning: (e) => {
      Fe(this).warn(e);
    } };
  }
  _makeTokenCache(e) {
    return new zg(e, (t) => {
      Fe(this).debug("advisory token refresh failed; serving cached token", t);
    });
  }
  withOptions(e) {
    let t = "credentials" in e || "config" in e || "profile" in e, r = "apiKey" in e || "authToken" in e || t, o = { ...this._options, ...this._baseURLIsExplicit ? { baseURL: this.baseURL } : {}, maxRetries: this.maxRetries, timeout: this.timeout, logger: this.logger, logLevel: this.logLevel, fetch: this.fetch, fetchOptions: this.fetchOptions, apiKey: this.apiKey, authToken: this.authToken, credentials: this.credentials, ...t ? { credentials: void 0, config: void 0, profile: void 0 } : {}, ...e, __auth: r ? void 0 : this._authState, __baseURLIsExplicit: "baseURL" in e ? true : this._baseURLIsExplicit };
    return new this.constructor(o);
  }
  async _resolveDefaultCredentials(e) {
    try {
      let t = await Ek(this._credentialResolverOptions(), e);
      if (t) this._authState.provider = t.provider, this._authState.tokenCache = this._makeTokenCache(t.provider), this._authState.extraHeaders = t.extraHeaders, this._applyCredentialBaseURL(t.baseURL);
      else if (e != null) throw new z(`Profile "${e}" could not be resolved (no <config_dir>/configs/${e}.json found).`);
    } catch (t) {
      this._authState.error = t;
    } finally {
      this._authState.resolution = null;
    }
  }
  defaultQuery() {
    return this._options.defaultQuery;
  }
  validateHeaders({ values: e, nulls: t }) {
    if (e.get("x-api-key") || e.get("authorization")) return;
    if (this._authState.error) throw this._authState.error;
    if (this._authState.tokenCache || this._authState.resolution) return;
    if (this.apiKey && e.get("x-api-key")) return;
    if (t.has("x-api-key")) return;
    if (this.authToken && e.get("authorization")) return;
    if (t.has("authorization")) return;
    throw Error('Could not resolve authentication method. Expected one of apiKey, authToken, credentials, config, or profile to be set. Or for one of the "X-Api-Key" or "Authorization" headers to be explicitly omitted');
  }
  _authFlags(e) {
    let t = this._requestAuthFlags.get(e);
    if (!t) t = { usedTokenCache: false, didRefreshFor401: false }, this._requestAuthFlags.set(e, t);
    return t;
  }
  async authHeaders(e) {
    if (this._authState.resolution) await this._authState.resolution;
    if (this._authState.error) return;
    if (this._authState.tokenCache && this.apiKey == null) {
      let t = await this._authState.tokenCache.getToken();
      return this._authFlags(e).usedTokenCache = true, E([{ Authorization: `Bearer ${t}` }]);
    }
    return E([await this.apiKeyAuth(e), await this.bearerAuth(e)]);
  }
  async apiKeyAuth(e) {
    if (this.apiKey == null) return;
    return E([{ "X-Api-Key": this.apiKey }]);
  }
  async bearerAuth(e) {
    if (this.authToken == null) return;
    return E([{ Authorization: `Bearer ${this.authToken}` }]);
  }
  stringifyQuery(e) {
    return ak(e);
  }
  getUserAgent() {
    return `${this.constructor.name}/JS ${Zt}`;
  }
  defaultIdempotencyKey() {
    return `stainless-node-retry-${Cg()}`;
  }
  makeStatusError(e, t, r, o) {
    return We.generate(e, t, r, o);
  }
  buildURL(e, t, r) {
    let o = !_(this, ph, "m", Qk).call(this) && r || this.baseURL, n = Jw(e) ? new URL(e) : new URL(o + (o.endsWith("/") && e.startsWith("/") ? e.slice(1) : e)), i = this.defaultQuery(), s = Object.fromEntries(n.searchParams);
    if (!Ng(i) || !Ng(s)) t = { ...s, ...i, ...t };
    if (typeof t === "object" && t && !Array.isArray(t)) n.search = this.stringifyQuery(t);
    return n.toString();
  }
  _calculateNonstreamingTimeout(e) {
    if (3600 * e / 128e3 > 600) throw new z("Streaming is required for operations that may take longer than 10 minutes. See https://github.com/anthropics/anthropic-sdk-typescript#streaming-responses for more details");
    return 6e5;
  }
  async prepareOptions(e) {
  }
  async prepareRequest(e, { url: t, options: r }) {
    if (this._authState.tokenCache && this.apiKey == null) {
      let o = e.headers instanceof Headers ? e.headers : new Headers(e.headers);
      for (let [i, s] of Object.entries(this._authState.extraHeaders)) if (!o.has(i)) o.set(i, s);
      if (!o.get("anthropic-beta")?.split(",").map((i) => i.trim())?.includes(ro)) o.append("anthropic-beta", ro);
      e.headers = o;
    }
  }
  get(e, t) {
    return this.methodRequest("get", e, t);
  }
  post(e, t) {
    return this.methodRequest("post", e, t);
  }
  patch(e, t) {
    return this.methodRequest("patch", e, t);
  }
  put(e, t) {
    return this.methodRequest("put", e, t);
  }
  delete(e, t) {
    return this.methodRequest("delete", e, t);
  }
  methodRequest(e, t, r) {
    return this.request(Promise.resolve(r).then((o) => ({ method: e, path: t, ...o })));
  }
  request(e, t = null) {
    return new no(this, this.makeRequest(e, t, void 0));
  }
  async makeRequest(e, t, r) {
    let o = await e, n = o.maxRetries ?? this.maxRetries;
    if (t == null) t = n, this._requestAuthFlags.delete(o);
    await this.prepareOptions(o);
    let { req: i, url: s, timeout: a } = await this.buildRequest(o, { retryCount: n - t });
    await this.prepareRequest(i, { url: s, options: o });
    let c = "log_" + (Math.random() * 16777216 | 0).toString(16).padStart(6, "0"), u = r === void 0 ? "" : `, retryOf: ${r}`, d = Date.now();
    if (Fe(this).debug(`[${c}] sending request`, Dr({ retryOfRequestLogID: r, method: o.method, url: s, options: o, headers: i.headers })), o.signal?.aborted) throw new et();
    let p = new AbortController(), f = await this.fetchWithTimeout(s, i, a, p).catch(Ks), m = Date.now();
    if (f instanceof globalThis.Error) {
      let y = `retrying, ${t} attempts remaining`;
      if (o.signal?.aborted) throw new et();
      let v = Mr(f) || /timed? ?out/i.test(String(f) + ("cause" in f ? String(f.cause) : ""));
      if (t) return Fe(this).info(`[${c}] connection ${v ? "timed out" : "failed"} - ${y}`), Fe(this).debug(`[${c}] connection ${v ? "timed out" : "failed"} (${y})`, Dr({ retryOfRequestLogID: r, url: s, durationMs: m - d, message: f.message })), this.retryRequest(o, t, r ?? c);
      if (Fe(this).info(`[${c}] connection ${v ? "timed out" : "failed"} - error; no more retries left`), Fe(this).debug(`[${c}] connection ${v ? "timed out" : "failed"} (error; no more retries left)`, Dr({ retryOfRequestLogID: r, url: s, durationMs: m - d, message: f.message })), v) throw new Gs();
      throw new to({ cause: f });
    }
    let g = [...f.headers.entries()].filter(([y]) => y === "request-id").map(([y, v]) => ", " + y + ": " + JSON.stringify(v)).join(""), h = `[${c}${u}${g}] ${i.method} ${s} ${f.ok ? "succeeded" : "failed"} with status ${f.status} in ${m - d}ms`;
    if (!f.ok) {
      let y = await this.shouldRetry(f, o);
      if (t && y) {
        let se = `retrying, ${t} attempts remaining`;
        return await ik(f.body), Fe(this).info(`${h} - ${se}`), Fe(this).debug(`[${c}] response error (${se})`, Dr({ retryOfRequestLogID: r, url: f.url, status: f.status, headers: f.headers, durationMs: m - d })), this.retryRequest(o, t, r ?? c, f.headers);
      }
      let v = y ? "error; no more retries left" : "error; not retryable";
      Fe(this).info(`${h} - ${v}`);
      let x = await f.text().catch((se) => Ks(se).message), w = ku(x), A = w ? void 0 : x;
      throw Fe(this).debug(`[${c}] response error (${v})`, Dr({ retryOfRequestLogID: r, url: f.url, status: f.status, headers: f.headers, message: A, durationMs: Date.now() - d })), this.makeStatusError(f.status, w, A, f.headers);
    }
    return Fe(this).info(h), Fe(this).debug(`[${c}] response start`, Dr({ retryOfRequestLogID: r, url: f.url, status: f.status, headers: f.headers, durationMs: m - d })), { response: f, options: o, controller: p, requestLogID: c, retryOfRequestLogID: r, startTime: d };
  }
  getAPIList(e, t, r) {
    return this.requestAPIList(t, r && "then" in r ? r.then((o) => ({ method: "get", path: e, ...o })) : { method: "get", path: e, ...r });
  }
  requestAPIList(e, t) {
    let r = this.makeRequest(t, null, void 0);
    return new Nu(this, r, e);
  }
  async fetchWithTimeout(e, t, r, o) {
    let { signal: n, method: i, ...s } = t || {}, a = this._makeAbort(o);
    if (n) n.addEventListener("abort", a, { once: true });
    let c = setTimeout(a, r), u = globalThis.ReadableStream && s.body instanceof globalThis.ReadableStream || typeof s.body === "object" && s.body !== null && Symbol.asyncIterator in s.body, d = { signal: o.signal, ...u ? { duplex: "half" } : {}, method: "GET", ...s };
    if (i) d.method = i.toUpperCase();
    try {
      return await this.fetch.call(void 0, e, d);
    } finally {
      clearTimeout(c);
    }
  }
  async shouldRetry(e, t) {
    let r = this._authFlags(t);
    if (e.status === 401 && this._authState.tokenCache && r.usedTokenCache && !r.didRefreshFor401) return r.didRefreshFor401 = true, this._authState.tokenCache.invalidate(), true;
    let o = e.headers.get("x-should-retry");
    if (o === "true") return true;
    if (o === "false") return false;
    if (e.status === 408) return true;
    if (e.status === 409) return true;
    if (e.status === 429) return true;
    if (e.status >= 500) return true;
    return false;
  }
  async retryRequest(e, t, r, o) {
    let n, i = o?.get("retry-after-ms");
    if (i) {
      let a = parseFloat(i);
      if (!Number.isNaN(a)) n = a;
    }
    let s = o?.get("retry-after");
    if (s && !n) {
      let a = parseFloat(s);
      if (!Number.isNaN(a)) n = a * 1e3;
      else n = Date.parse(s) - Date.now();
    }
    if (n === void 0) {
      let a = e.maxRetries ?? this.maxRetries;
      n = this.calculateDefaultRetryTimeoutMillis(t, a);
    }
    return await Qw(n), this.makeRequest(e, t - 1, r);
  }
  calculateDefaultRetryTimeoutMillis(e, t) {
    let n = t - e, i = Math.min(0.5 * Math.pow(2, n), 8), s = 1 - Math.random() * 0.25;
    return i * s * 1e3;
  }
  calculateNonstreamingTimeout(e, t) {
    if (36e5 * e / 128e3 > 6e5 || t != null && e > t) throw new z("Streaming is required for operations that may take longer than 10 minutes. See https://github.com/anthropics/anthropic-sdk-typescript#long-requests for more details");
    return 6e5;
  }
  async buildRequest(e, { retryCount: t = 0 } = {}) {
    let r = { ...e }, { method: o, path: n, query: i, defaultBaseURL: s } = r;
    if (this._authState.resolution) await this._authState.resolution;
    if (!this._baseURLIsExplicit && this._authState.baseURL && this.baseURL !== this._authState.baseURL) this.baseURL = this._authState.baseURL;
    let a = this.buildURL(n, i, s);
    if ("timeout" in r) Yw("timeout", r.timeout);
    r.timeout = r.timeout ?? this.timeout;
    let { bodyHeaders: c, body: u } = this.buildBody({ options: r }), d = await this.buildHeaders({ options: e, method: o, bodyHeaders: c, retryCount: t });
    return { req: { method: o, headers: d, ...r.signal && { signal: r.signal }, ...globalThis.ReadableStream && u instanceof globalThis.ReadableStream && { duplex: "half" }, ...u && { body: u }, ...this.fetchOptions ?? {}, ...r.fetchOptions ?? {} }, url: a, timeout: r.timeout };
  }
  async buildHeaders({ options: e, method: t, bodyHeaders: r, retryCount: o }) {
    let n = {};
    if (this.idempotencyHeader && t !== "get") {
      if (!e.idempotencyKey) e.idempotencyKey = this.defaultIdempotencyKey();
      n[this.idempotencyHeader] = e.idempotencyKey;
    }
    let i = E([n, { Accept: "application/json", "User-Agent": this.getUserAgent(), "X-Stainless-Retry-Count": String(o), ...e.timeout ? { "X-Stainless-Timeout": String(Math.trunc(e.timeout / 1e3)) } : {}, ...oa(), ...this._options.dangerouslyAllowBrowser ? { "anthropic-dangerous-direct-browser-access": "true" } : void 0, "anthropic-version": "2023-06-01" }, await this.authHeaders(e), this._options.defaultHeaders, r, e.headers]);
    return this.validateHeaders(i), i.values;
  }
  _makeAbort(e) {
    return () => e.abort();
  }
  buildBody({ options: { body: e, headers: t } }) {
    if (!e) return { bodyHeaders: void 0, body: void 0 };
    let r = E([t]);
    if (ArrayBuffer.isView(e) || e instanceof ArrayBuffer || e instanceof DataView || typeof e === "string" && r.values.has("content-type") || globalThis.Blob && e instanceof globalThis.Blob || e instanceof FormData || e instanceof URLSearchParams || globalThis.ReadableStream && e instanceof globalThis.ReadableStream) return { bodyHeaders: void 0, body: e };
    else if (typeof e === "object" && (Symbol.asyncIterator in e || Symbol.iterator in e && "next" in e && typeof e.next === "function")) return { bodyHeaders: void 0, body: Eu(e) };
    else if (typeof e === "object" && r.values.get("content-type") === "application/x-www-form-urlencoded") return { bodyHeaders: { "content-type": "application/x-www-form-urlencoded" }, body: this.stringifyQuery(e) };
    else return _(this, td, "f").call(this, { body: e, headers: r });
  }
};
fh = Ue, td = /* @__PURE__ */ new WeakMap(), ph = /* @__PURE__ */ new WeakSet(), Qk = function() {
  return this.baseURL !== "https://api.anthropic.com";
};
Ue.Anthropic = fh;
Ue.HUMAN_PROMPT = eE;
Ue.AI_PROMPT = tE;
Ue.DEFAULT_TIMEOUT = 6e5;
Ue.AnthropicError = z;
Ue.APIError = We;
Ue.APIConnectionError = to;
Ue.APIConnectionTimeoutError = Gs;
Ue.APIUserAbortError = et;
Ue.NotFoundError = Qs;
Ue.ConflictError = ea;
Ue.RateLimitError = ra;
Ue.BadRequestError = Js;
Ue.AuthenticationError = Xs;
Ue.InternalServerError = na;
Ue.PermissionDeniedError = Ys;
Ue.UnprocessableEntityError = ta;
Ue.toFile = ju;
var po = class extends Ue {
  constructor() {
    super(...arguments);
    this.completions = new mi(this), this.messages = new uo(this), this.models = new hi(this), this.beta = new it(this);
  }
};
po.Completions = mi;
po.Messages = uo;
po.Models = hi;
po.Beta = it;
function cF(e) {
  return e;
}
function yi(e) {
  return cF(e);
}
function kr(e) {
  return e instanceof Error ? e : Error(String(e));
}
function bi(e) {
  return e instanceof Error ? e.message : String(e);
}
function Ge(e) {
  if (e && typeof e === "object" && "code" in e && typeof e.code === "string") return e.code;
  return;
}
function zr(e) {
  return Ge(e) === "ENOENT";
}
function mh(e) {
  return Ge(e) === "EISDIR";
}
function rE(e) {
  let t = Ge(e);
  return t === "ENOENT" || t === "EACCES" || t === "EPERM" || t === "ENOTDIR" || t === "ELOOP" || t === "EROFS";
}
var mF = /* @__PURE__ */ new Set(["EXDEV", "EPERM", "EEXIST", "EBUSY"]);
var gF = /* @__PURE__ */ new Set(["ENOSPC", "EIO", "EDQUOT", "EFBIG"]);
async function nE(e, t, r) {
  let o = `${e}.tmp.${lF(4).toString("hex")}`;
  try {
    await fF(o, t, { encoding: "utf8", mode: r });
    try {
      await pF(o, e);
    } catch (n) {
      let i = Ge(n);
      if (i !== void 0 && mF.has(i)) {
        try {
          if (await dF(o, e), r !== void 0) await uF(e, r).catch(() => {
          });
        } catch (s) {
          if (gF.has(Ge(s) ?? "")) await gh(e).catch(() => {
          });
          throw s;
        }
        await gh(o).catch(() => {
        });
      } else throw n;
    }
  } catch (n) {
    throw await gh(o).catch(() => {
    }), n;
  }
}
var cE = class {
  read(e) {
    return sE(e, "utf8");
  }
  readBytes(e) {
    return sE(e);
  }
  write(e, t, r) {
    return hh(e, t, { encoding: "utf8", mode: r });
  }
  async mkdir(e) {
    try {
      await _F(e, { recursive: true });
    } catch (t) {
      if (Ge(t) !== "EEXIST") throw t;
    }
  }
  atomicWrite(e, t, r) {
    return nE(e, t, r);
  }
  delete(e) {
    return SF(e);
  }
  list(e) {
    return iE(e);
  }
  append(e, t, r) {
    return yF(e, t, { encoding: "utf8", mode: r });
  }
  writeExclusive(e, t, r) {
    return hh(e, t, { encoding: "utf8", flag: "wx", mode: r });
  }
  writeBytes(e, t) {
    return hh(e, t);
  }
  copy(e, t) {
    return bF(e, t);
  }
  async stat(e) {
    return { mtimeMs: (await vF(e)).mtimeMs };
  }
  async listEntries(e) {
    return (await iE(e, { withFileTypes: true })).map((r) => ({ name: r.name, isDirectory: r.isDirectory(), isFile: r.isFile() }));
  }
  async readRange(e, t, r) {
    yh("readRange", "offset", t), yh("readRange", "length", r);
    let o = await oE(e, "r");
    try {
      return await aE(o, t, r);
    } finally {
      await o.close();
    }
  }
  async readTail(e, t) {
    yh("readTail", "maxBytes", t);
    let r = await oE(e, "r");
    try {
      let { size: o } = await r.stat(), n = Math.min(t, o);
      return await aE(r, o - n, n);
    } finally {
      await r.close();
    }
  }
};
function yh(e, t, r) {
  if (!Number.isInteger(r) || r < 0) throw RangeError(`${e}: ${t} must be a non-negative integer, got ${r}`);
}
async function aE(e, t, r) {
  if (r === 0) return Buffer.alloc(0);
  let o = Buffer.alloc(r), n = 0;
  while (n < r) {
    let { bytesRead: i } = await e.read(o, n, r - n, t + n);
    if (i === 0) break;
    n += i;
  }
  return n === r ? o : Buffer.from(o.subarray(0, n));
}
var xF = new hF();
function _i() {
  return xF.getStore() ?? new cE();
}
var fo;
var vi = null;
function uE() {
  if (vi) return vi;
  if (!Ee(process.env.DEBUG_CLAUDE_AGENT_SDK)) return fo = null, vi = Promise.resolve(), vi;
  let e = lE(qt(), "debug");
  return fo = lE(e, `sdk-${wF()}.txt`), process.stderr.write(`SDK debug logs: ${fo}
`), vi = _i().mkdir(e).catch(() => {
  }), vi;
}
function dE() {
  return uE(), fo ?? null;
}
function vt(e) {
  if (fo === null) return;
  let r = `${(/* @__PURE__ */ new Date()).toISOString()} ${e}
`;
  uE().then(() => {
    if (fo) _i().append(fo, r).catch(() => {
    });
  });
}
function kF() {
  this.__data__ = new fn(), this.size = 0;
}
var pE = kF;
function EF(e) {
  var t = this.__data__, r = t.delete(e);
  return this.size = t.size, r;
}
var fE = EF;
function TF(e) {
  return this.__data__.get(e);
}
var mE = TF;
function PF(e) {
  return this.__data__.has(e);
}
var gE = PF;
var IF = 200;
function RF(e, t) {
  var r = this.__data__;
  if (r instanceof fn) {
    var o = r.__data__;
    if (!xu || o.length < IF - 1) return o.push([e, t]), this.size = ++r.size, this;
    r = this.__data__ = new Ws(o);
  }
  return r.set(e, t), this.size = r.size, this;
}
var hE = RF;
function Si(e) {
  var t = this.__data__ = new fn(e);
  this.size = t.size;
}
Si.prototype.clear = pE;
Si.prototype.delete = fE;
Si.prototype.get = mE;
Si.prototype.has = gE;
Si.prototype.set = hE;
var yE = Si;
var $F = function() {
  try {
    var e = Qo(Object, "defineProperty");
    return e({}, "", {}), e;
  } catch (t) {
  }
}();
var xi = $F;
function OF(e, t, r) {
  if (t == "__proto__" && xi) xi(e, t, { configurable: true, enumerable: true, value: r, writable: true });
  else e[t] = r;
}
var wi = OF;
var AF = Object.prototype;
var CF = AF.hasOwnProperty;
function MF(e, t, r) {
  var o = e[t];
  if (!(CF.call(e, t) && dn(o, r)) || r === void 0 && !(t in e)) wi(e, t, r);
}
var rd = MF;
function DF(e, t, r, o) {
  var n = !r;
  r || (r = {});
  var i = -1, s = t.length;
  while (++i < s) {
    var a = t[i], c = o ? o(r[a], e[a], a, r, e) : void 0;
    if (c === void 0) c = e[a];
    if (n) wi(r, a, c);
    else rd(r, a, c);
  }
  return r;
}
var bE = DF;
function NF(e, t) {
  var r = -1, o = Array(e);
  while (++r < e) o[r] = t(r);
  return o;
}
var _E = NF;
function jF(e) {
  return e != null && typeof e == "object";
}
var Kt = jF;
var UF = "[object Arguments]";
function zF(e) {
  return Kt(e) && wr(e) == UF;
}
var bh = zF;
var vE = Object.prototype;
var LF = vE.hasOwnProperty;
var FF = vE.propertyIsEnumerable;
var BF = bh(/* @__PURE__ */ function() {
  return arguments;
}()) ? bh : function(e) {
  return Kt(e) && LF.call(e, "callee") && !FF.call(e, "callee");
};
var Lr = BF;
var HF = Array.isArray;
var dt = HF;
var od = {};
xr(od, { default: () => Fa });
function qF() {
  return false;
}
var SE = qF;
var kE = typeof od == "object" && od && !od.nodeType && od;
var xE = kE && typeof nd == "object" && nd && !nd.nodeType && nd;
var ZF = xE && xE.exports === kE;
var wE = ZF ? Bt.Buffer : void 0;
var VF = wE ? wE.isBuffer : void 0;
var WF = VF || SE;
var Fa = WF;
var KF = 9007199254740991;
var GF = /^(?:0|[1-9]\d*)$/;
function JF(e, t) {
  var r = typeof e;
  return t = t == null ? KF : t, !!t && (r == "number" || r != "symbol" && GF.test(e)) && (e > -1 && e % 1 == 0 && e < t);
}
var vn = JF;
var XF = 9007199254740991;
function YF(e) {
  return typeof e == "number" && e > -1 && e % 1 == 0 && e <= XF;
}
var ki = YF;
var QF = "[object Arguments]";
var e4 = "[object Array]";
var t4 = "[object Boolean]";
var r4 = "[object Date]";
var n4 = "[object Error]";
var o4 = "[object Function]";
var i4 = "[object Map]";
var s4 = "[object Number]";
var a4 = "[object Object]";
var c4 = "[object RegExp]";
var l4 = "[object Set]";
var u4 = "[object String]";
var d4 = "[object WeakMap]";
var p4 = "[object ArrayBuffer]";
var f4 = "[object DataView]";
var m4 = "[object Float32Array]";
var g4 = "[object Float64Array]";
var h4 = "[object Int8Array]";
var y4 = "[object Int16Array]";
var b4 = "[object Int32Array]";
var _4 = "[object Uint8Array]";
var v4 = "[object Uint8ClampedArray]";
var S4 = "[object Uint16Array]";
var x4 = "[object Uint32Array]";
var $e = {};
$e[m4] = $e[g4] = $e[h4] = $e[y4] = $e[b4] = $e[_4] = $e[v4] = $e[S4] = $e[x4] = true;
$e[QF] = $e[e4] = $e[p4] = $e[t4] = $e[f4] = $e[r4] = $e[n4] = $e[o4] = $e[i4] = $e[s4] = $e[a4] = $e[c4] = $e[l4] = $e[u4] = $e[d4] = false;
function w4(e) {
  return Kt(e) && ki(e.length) && !!$e[wr(e)];
}
var EE = w4;
function k4(e) {
  return function(t) {
    return e(t);
  };
}
var TE = k4;
var sd = {};
xr(sd, { default: () => ad });
var PE = typeof sd == "object" && sd && !sd.nodeType && sd;
var Ba = PE && typeof id == "object" && id && !id.nodeType && id;
var E4 = Ba && Ba.exports === PE;
var _h = E4 && vu.process;
var T4 = function() {
  try {
    var e = Ba && Ba.require && Ba.require("util").types;
    if (e) return e;
    return _h && _h.binding && _h.binding("util");
  } catch (t) {
  }
}();
var ad = T4;
var IE = ad && ad.isTypedArray;
var P4 = IE ? TE(IE) : EE;
var cd = P4;
var I4 = Object.prototype;
var R4 = I4.hasOwnProperty;
function $4(e, t) {
  var r = dt(e), o = !r && Lr(e), n = !r && !o && Fa(e), i = !r && !o && !n && cd(e), s = r || o || n || i, a = s ? _E(e.length, String) : [], c = a.length;
  for (var u in e) if ((t || R4.call(e, u)) && !(s && (u == "length" || n && (u == "offset" || u == "parent") || i && (u == "buffer" || u == "byteLength" || u == "byteOffset") || vn(u, c)))) a.push(u);
  return a;
}
var RE = $4;
var O4 = Object.prototype;
function A4(e) {
  var t = e && e.constructor, r = typeof t == "function" && t.prototype || O4;
  return e === r;
}
var ld = A4;
function C4(e, t) {
  return function(r) {
    return e(t(r));
  };
}
var $E = C4;
function M4(e) {
  return e != null && ki(e.length) && !Yo(e);
}
var Ei = M4;
function D4(e) {
  var t = [];
  if (e != null) for (var r in Object(e)) t.push(r);
  return t;
}
var OE = D4;
var N4 = Object.prototype;
var j4 = N4.hasOwnProperty;
function U4(e) {
  if (!Qe(e)) return OE(e);
  var t = ld(e), r = [];
  for (var o in e) if (!(o == "constructor" && (t || !j4.call(e, o)))) r.push(o);
  return r;
}
var AE = U4;
function z4(e) {
  return Ei(e) ? RE(e, true) : AE(e);
}
var ud = z4;
var pd = {};
xr(pd, { default: () => vh });
var NE = typeof pd == "object" && pd && !pd.nodeType && pd;
var CE = NE && typeof dd == "object" && dd && !dd.nodeType && dd;
var L4 = CE && CE.exports === NE;
var ME = L4 ? Bt.Buffer : void 0;
var DE = ME ? ME.allocUnsafe : void 0;
function F4(e, t) {
  if (t) return e.slice();
  var r = e.length, o = DE ? DE(r) : new e.constructor(r);
  return e.copy(o), o;
}
var vh = F4;
function B4(e, t) {
  var r = -1, o = e.length;
  t || (t = Array(o));
  while (++r < o) t[r] = e[r];
  return t;
}
var jE = B4;
function H4(e, t) {
  var r = -1, o = t.length, n = e.length;
  while (++r < o) e[n + r] = t[r];
  return e;
}
var UE = H4;
var q4 = $E(Object.getPrototypeOf, Object);
var fd = q4;
var Z4 = Bt.Uint8Array;
var Sh = Z4;
function V4(e) {
  var t = new e.constructor(e.byteLength);
  return new Sh(t).set(new Sh(e)), t;
}
var zE = V4;
function W4(e, t) {
  var r = t ? zE(e.buffer) : e.buffer;
  return new e.constructor(r, e.byteOffset, e.length);
}
var LE = W4;
var FE = Object.create;
var K4 = /* @__PURE__ */ function() {
  function e() {
  }
  return function(t) {
    if (!Qe(t)) return {};
    if (FE) return FE(t);
    e.prototype = t;
    var r = new e();
    return e.prototype = void 0, r;
  };
}();
var BE = K4;
function G4(e) {
  return typeof e.constructor == "function" && !ld(e) ? BE(fd(e)) : {};
}
var HE = G4;
var J4 = "[object Symbol]";
function X4(e) {
  return typeof e == "symbol" || Kt(e) && wr(e) == J4;
}
var Ti = X4;
var Y4 = /\.|\[(?:[^[\]]*|(["'])(?:(?!\1)[^\\]|\\.)*?\1)\]/;
var Q4 = /^\w*$/;
function e2(e, t) {
  if (dt(e)) return false;
  var r = typeof e;
  if (r == "number" || r == "symbol" || r == "boolean" || e == null || Ti(e)) return true;
  return Q4.test(e) || !Y4.test(e) || t != null && e in Object(t);
}
var qE = e2;
var t2 = 500;
function r2(e) {
  var t = Ce(e, function(o) {
    if (r.size === t2) r.clear();
    return o;
  }), r = t.cache;
  return t;
}
var ZE = r2;
var n2 = /[^.[\]]+|\[(?:(-?\d+(?:\.\d+)?)|(["'])((?:(?!\2)[^\\]|\\.)*?)\2)\]|(?=(?:\.|\[\])(?:\.|\[\]|$))/g;
var o2 = /\\(\\)?/g;
var i2 = ZE(function(e) {
  var t = [];
  if (e.charCodeAt(0) === 46) t.push("");
  return e.replace(n2, function(r, o, n, i) {
    t.push(n ? i.replace(o2, "$1") : o || r);
  }), t;
});
var VE = i2;
function s2(e, t) {
  var r = -1, o = e == null ? 0 : e.length, n = Array(o);
  while (++r < o) n[r] = t(e[r], r, e);
  return n;
}
var WE = s2;
var a2 = 1 / 0;
var KE = Ht ? Ht.prototype : void 0;
var GE = KE ? KE.toString : void 0;
function JE(e) {
  if (typeof e == "string") return e;
  if (dt(e)) return WE(e, JE) + "";
  if (Ti(e)) return GE ? GE.call(e) : "";
  var t = e + "";
  return t == "0" && 1 / e == -a2 ? "-0" : t;
}
var XE = JE;
function c2(e) {
  return e == null ? "" : XE(e);
}
var YE = c2;
function l2(e, t) {
  if (dt(e)) return e;
  return qE(e, t) ? [e] : VE(YE(e));
}
var Sn = l2;
var u2 = 1 / 0;
function d2(e) {
  if (typeof e == "string" || Ti(e)) return e;
  var t = e + "";
  return t == "0" && 1 / e == -u2 ? "-0" : t;
}
var Pi = d2;
function p2(e, t) {
  t = Sn(t, e);
  var r = 0, o = t.length;
  while (e != null && r < o) e = e[Pi(t[r++])];
  return r && r == o ? e : void 0;
}
var QE = p2;
function f2(e, t) {
  return e != null && t in Object(e);
}
var eT = f2;
function m2(e, t, r) {
  t = Sn(t, e);
  var o = -1, n = t.length, i = false;
  while (++o < n) {
    var s = Pi(t[o]);
    if (!(i = e != null && r(e, s))) break;
    e = e[s];
  }
  if (i || ++o != n) return i;
  return n = e == null ? 0 : e.length, !!n && ki(n) && vn(s, n) && (dt(e) || Lr(e));
}
var tT = m2;
function g2(e, t) {
  return e != null && tT(e, t, eT);
}
var rT = g2;
function h2(e) {
  return e;
}
var md = h2;
function uT() {
  return { sent: /* @__PURE__ */ new Set(), rejected: /* @__PURE__ */ new Set() };
}
var dT = "[\\w-]{1,63}";
var Zfe = new RegExp(`^${dT}$`);
var Vfe = new RegExp(`^a(?:${dT}-)?[0-9a-f]{16}$`);
var k2 = { renderTarget: "ink", workspace: "local", canDrive: true, transcriptSource: "local-jsonl", remote: null };
function E2() {
  let e = "";
  if (typeof process < "u" && typeof process.cwd === "function" && typeof pT === "function") {
    let r = w2();
    try {
      e = fT(pT(r));
    } catch {
      e = fT(r);
    }
  }
  return { originalCwd: e, projectRoot: e, totalCostUSD: 0, totalAPIDuration: 0, totalAPIDurationWithoutRetries: 0, totalToolDuration: 0, startTime: Date.now(), lastInteractionTime: Date.now(), totalLinesAdded: 0, totalLinesRemoved: 0, hasUnknownModelCost: false, cwd: e, modelUsage: {}, mainLoopModelOverride: void 0, refusalFallbackOccurred: false, refusalFallbackModelLatch: void 0, sdkDialogHostActive: false, sdkSupportedDialogKinds: void 0, sdkSupportedDialogKindsSource: void 0, replConfigArgv: [], initialMainLoopModel: void 0, modelStrings: null, isInteractive: false, attacherCaps: null, hasStreamingInput: false, modelOverrideOptOutForSession: false, kairosActive: false, rendererMode: void 0, strictToolResultPairing: false, memoryToggledOff: false, teamMemoryServerStatus: void 0, sdkAgentProgressSummariesEnabled: false, userMsgOptIn: false, searchToolsOptIn: false, clientType: "cli", sessionSource: void 0, sessionStartType: "fresh", questionPreviewFormat: void 0, sessionIngressToken: void 0, oauthTokenFromFd: void 0, oauthScopesFromFd: void 0, apiKeyFromFd: void 0, gatewayAuth: null, gatewayRefreshInFlight: null, flagSettingsPath: void 0, flagSettingsExpectedContent: void 0, flagSettingsInline: null, parentManagedSettings: null, allowedSettingSources: ["userSettings", "projectSettings", "localSettings", "flagSettings", "policySettings"], meter: null, sessionCounter: null, locCounter: null, prCounter: null, commitCounter: null, costCounter: null, tokenCounter: null, codeEditToolDecisionCounter: null, activeTimeCounter: null, statsStore: null, sessionId: Ha(), mainAgentId: null, parentSessionId: void 0, loggerProvider: null, eventLogger: null, pendingOTelEvents: [], meterProvider: null, tracerProvider: null, cachedTelemetryResource: null, cachedOtlpHttpAgentFactory: { direct: null, proxied: null }, foundryDeploymentCapabilities: /* @__PURE__ */ new Map(), agentColorMap: /* @__PURE__ */ new Map(), agentColorIndex: 0, lastAPIRequest: null, lastCancelledAPIMessageId: null, lastAPIRequestMessages: null, lastClassifierRequests: null, cachedClaudeMdContent: null, inMemoryErrorLog: [], inlinePlugins: [], inlinePluginsNoMcp: [], inlinePluginUrls: [], syncedPluginDirs: [], chromeFlagOverride: void 0, onboardingShownThisSession: false, useCoworkPlugins: false, disableSlashCommands: false, sessionBypassPermissionsMode: false, scheduledTasksEnabled: false, sessionPrResolved: false, sessionCronTasks: [], loopChainStartedAt: /* @__PURE__ */ Object.create(null), loopTickInFlightPrompt: null, loopConsecutiveKeepalives: 0, sessionCreatedTeams: /* @__PURE__ */ new Set(), inheritedTeamName: void 0, sessionTrustAccepted: false, sessionPersistenceDisabled: false, hasExitedPlanMode: false, needsPlanModeExitAttachment: false, needsAutoModeExitAttachment: false, lspRecommendationShownThisSession: false, initJsonSchema: null, registeredHooks: null, planSlugCache: /* @__PURE__ */ new Map(), teleportedSessionInfo: null, invokedSkills: /* @__PURE__ */ new Map(), slowOperations: [], sdkBetas: void 0, longContext1mCreditsBlocked: false, fableCreditsRequired: false, fableConsentSessionFallback: false, fableBridgeDialogTimedOut: false, fableConsentDialogInteracted: false, sdkOAuthTokenRefreshCallback: null, hostAuthTokenRefreshCallback: null, mainThreadAgentType: void 0, mainThreadAgentHooks: void 0, sessionSkillAllowlist: void 0, caps: k2, replBridgeActive: false, directConnectServerUrl: void 0, mcpConnectNonBlocking: false, strictMcpConfig: false, activeRoutine: void 0, systemPromptSectionCache: /* @__PURE__ */ new Map(), lastEmittedDate: null, additionalDirectoriesForClaudeMd: [], allowedChannels: [], activeInputs: /* @__PURE__ */ new Map(), hasDevChannels: false, sessionProjectDir: null, promptCache1hAllowlist: null, stickyBetas: uT(), thinkingTypeOverrides: /* @__PURE__ */ new Map(), inferenceProfileBackingModels: /* @__PURE__ */ new Map(), promptId: null, promptIndex: 0, lastMainRequestId: void 0, lastMainThreadCacheTtlMs: null, lastApiCompletionTimestamp: null, pendingPostCompaction: false };
}
var T2 = E2();
var P2 = () => {
  return;
};
function kh() {
  return P2()?.sessionId ?? T2.sessionId;
}
var I2 = un();
var rme = I2.subscribe;
function fT(e) {
  return process.platform === "darwin" ? e.normalize("NFC") : e;
}
var R2 = un();
var nme = R2.subscribe;
var $2 = un();
var ome = $2.subscribe;
var O2 = un();
var ime = O2.subscribe;
var A2 = un();
var sme = A2.subscribe;
function mT({ writeFn: e, flushIntervalMs: t = 1e3, maxBufferSize: r = 100, maxBufferBytes: o = 1 / 0, immediateMode: n = false }) {
  let i = [], s = 0, a = null, c = null;
  function u() {
    if (a) clearTimeout(a), a = null;
  }
  function d(g) {
    try {
      e(g);
    } catch {
    }
  }
  function p() {
    if (c) d(c.join("")), c = null;
    if (i.length === 0) return;
    d(i.join("")), i = [], s = 0, u();
  }
  function f() {
    if (!a) a = setTimeout(p, t);
  }
  function m() {
    if (c) {
      c.push(...i), i = [], s = 0, u();
      return;
    }
    let g = i;
    i = [], s = 0, u(), c = g, setImmediate(() => {
      let h = c;
      if (c = null, h) d(h.join(""));
    });
  }
  return { write(g) {
    if (n) {
      d(g);
      return;
    }
    if (i.push(g), s += g.length, f(), i.length >= r || s >= o) m();
  }, flush: p, dispose() {
    p();
  } };
}
function C2(e) {
  if (typeof e === "function") return e;
  if (Symbol.asyncDispose in e) return () => e[Symbol.asyncDispose]();
  return () => e[Symbol.dispose]();
}
var gT = class {
  #n = /* @__PURE__ */ new Set();
  register(e) {
    let t = C2(e);
    this.#n.add(t);
    let r = () => {
      this.#n.delete(t);
    };
    return Object.assign(r, { [Symbol.dispose]: r });
  }
  async drain() {
    let e = Array.from(this.#n);
    this.#n.clear(), await Promise.all(e.map(async (t) => t()));
  }
  async [Symbol.asyncDispose]() {
    await this.drain();
  }
  get sizeForTesting() {
    return this.#n.size;
  }
};
var M2 = new gT();
function hT(e) {
  return M2.register(e);
}
var yT = Ce((e) => {
  if (!e || e.trim() === "") return null;
  let t = e.split(",").map((i) => i.trim()).filter(Boolean);
  if (t.length === 0) return null;
  let r = t.some((i) => i.startsWith("!")), o = t.some((i) => !i.startsWith("!"));
  if (r && o) return null;
  let n = t.map((i) => i.replace(/^!/, "").toLowerCase());
  return { include: r ? [] : n, exclude: r ? n : [], isExclusive: r };
});
function D2(e) {
  let t = [], r = e.match(/^MCP server ["']([^"']+)["']/);
  if (r && r[1]) t.push("mcp"), t.push(r[1].toLowerCase());
  else {
    let i = e.match(/^([^:[]+):/);
    if (i && i[1]) t.push(i[1].trim().toLowerCase());
  }
  let o = e.match(/^\[([^\]]+)]/);
  if (o && o[1]) t.push(o[1].trim().toLowerCase());
  if (e.toLowerCase().includes("1p event:")) t.push("1p");
  let n = e.match(/:\s*([^:]+?)(?:\s+(?:type|mode|status|event))?:/);
  if (n && n[1]) {
    let i = n[1].trim().toLowerCase();
    if (i.length < 30 && !i.includes(" ")) t.push(i);
  }
  return Array.from(new Set(t));
}
function N2(e, t) {
  if (!t) return true;
  if (e.length === 0) return false;
  if (t.isExclusive) return !e.some((r) => t.exclude.includes(r));
  else return e.some((r) => t.include.includes(r));
}
function bT(e, t) {
  if (!t) return true;
  let r = D2(e);
  return N2(r, t);
}
var V2 = { cwd() {
  return process.cwd();
}, existsSync(e) {
  let r = [];
  try {
    const t = _e(r, Pe`fs.existsSync(${e})`, 0);
    return Y.existsSync(e);
  } catch (o) {
    var n = o, i = 1;
  } finally {
    ve(r, n, i);
  }
}, async stat(e) {
  return q2(e);
}, async lstat(e) {
  return j2(e);
}, async readdir(e) {
  return L2(e, { withFileTypes: true });
}, async unlink(e) {
  return Z2(e);
}, async rmdir(e) {
  return B2(e);
}, async rm(e, t) {
  return H2(e, t);
}, async mkdir(e, t) {
  try {
    await U2(e, { recursive: true, ...t });
  } catch (r) {
    if (Ge(r) !== "EEXIST") throw r;
  }
}, async readFile(e, t) {
  return _T(e, { encoding: t.encoding });
}, async rename(e, t) {
  return F2(e, t);
}, statSync(e) {
  let r = [];
  try {
    const t = _e(r, Pe`fs.statSync(${e})`, 0);
    return Y.statSync(e);
  } catch (o) {
    var n = o, i = 1;
  } finally {
    ve(r, n, i);
  }
}, lstatSync(e) {
  let r = [];
  try {
    const t = _e(r, Pe`fs.lstatSync(${e})`, 0);
    return Y.lstatSync(e);
  } catch (o) {
    var n = o, i = 1;
  } finally {
    ve(r, n, i);
  }
}, readFileSync(e, t) {
  let o = [];
  try {
    const r = _e(o, Pe`fs.readFileSync(${e})`, 0);
    return Y.readFileSync(e, { encoding: t.encoding });
  } catch (n) {
    var i = n, s = 1;
  } finally {
    ve(o, i, s);
  }
}, readFileBytesSync(e) {
  let r = [];
  try {
    const t = _e(r, Pe`fs.readFileBytesSync(${e})`, 0);
    return Y.readFileSync(e);
  } catch (o) {
    var n = o, i = 1;
  } finally {
    ve(r, n, i);
  }
}, readSync(e, t) {
  let n = [];
  try {
    const r = _e(n, Pe`fs.readSync(${e}, ${t.length} bytes)`, 0);
    let o = void 0;
    try {
      o = Y.openSync(e, "r");
      let c = Buffer.alloc(t.length), u = Y.readSync(o, c, 0, t.length, 0);
      return { buffer: c, bytesRead: u };
    } finally {
      if (o) Y.closeSync(o);
    }
  } catch (i) {
    var s = i, a = 1;
  } finally {
    ve(n, s, a);
  }
}, appendFileSync(e, t, r) {
  let n = [];
  try {
    const o = _e(n, Pe`fs.appendFileSync(${e}, ${t.length} chars)`, 0);
    if (r?.mode !== void 0) try {
      let c = Y.openSync(e, "ax", r.mode);
      try {
        Y.appendFileSync(c, t);
      } finally {
        Y.closeSync(c);
      }
      return;
    } catch (c) {
      if (Ge(c) !== "EEXIST") throw c;
    }
    Y.appendFileSync(e, t);
  } catch (i) {
    var s = i, a = 1;
  } finally {
    ve(n, s, a);
  }
}, copyFileSync(e, t) {
  let o = [];
  try {
    const r = _e(o, Pe`fs.copyFileSync(${e} → ${t})`, 0);
    Y.copyFileSync(e, t);
  } catch (n) {
    var i = n, s = 1;
  } finally {
    ve(o, i, s);
  }
}, unlinkSync(e) {
  let r = [];
  try {
    const t = _e(r, Pe`fs.unlinkSync(${e})`, 0);
    Y.unlinkSync(e);
  } catch (o) {
    var n = o, i = 1;
  } finally {
    ve(r, n, i);
  }
}, renameSync(e, t) {
  let o = [];
  try {
    const r = _e(o, Pe`fs.renameSync(${e} → ${t})`, 0);
    Y.renameSync(e, t);
  } catch (n) {
    var i = n, s = 1;
  } finally {
    ve(o, i, s);
  }
}, linkSync(e, t) {
  let o = [];
  try {
    const r = _e(o, Pe`fs.linkSync(${e} → ${t})`, 0);
    Y.linkSync(e, t);
  } catch (n) {
    var i = n, s = 1;
  } finally {
    ve(o, i, s);
  }
}, symlinkSync(e, t, r) {
  let n = [];
  try {
    const o = _e(n, Pe`fs.symlinkSync(${e} → ${t})`, 0);
    Y.symlinkSync(e, t, r);
  } catch (i) {
    var s = i, a = 1;
  } finally {
    ve(n, s, a);
  }
}, readlinkSync(e) {
  let r = [];
  try {
    const t = _e(r, Pe`fs.readlinkSync(${e})`, 0);
    return Y.readlinkSync(e);
  } catch (o) {
    var n = o, i = 1;
  } finally {
    ve(r, n, i);
  }
}, realpathSync(e) {
  let r = [];
  try {
    const t = _e(r, Pe`fs.realpathSync(${e})`, 0);
    return Or(Y.realpathSync(e));
  } catch (o) {
    var n = o, i = 1;
  } finally {
    ve(r, n, i);
  }
}, mkdirSync(e, t) {
  let n = [];
  try {
    const r = _e(n, Pe`fs.mkdirSync(${e})`, 0);
    let o = { recursive: true };
    if (t?.mode !== void 0) o.mode = t.mode;
    try {
      Y.mkdirSync(e, o);
    } catch (c) {
      if (Ge(c) !== "EEXIST") throw c;
    }
  } catch (i) {
    var s = i, a = 1;
  } finally {
    ve(n, s, a);
  }
}, readdirSync(e) {
  let r = [];
  try {
    const t = _e(r, Pe`fs.readdirSync(${e})`, 0);
    return Y.readdirSync(e, { withFileTypes: true });
  } catch (o) {
    var n = o, i = 1;
  } finally {
    ve(r, n, i);
  }
}, readdirStringSync(e) {
  let r = [];
  try {
    const t = _e(r, Pe`fs.readdirStringSync(${e})`, 0);
    return Y.readdirSync(e);
  } catch (o) {
    var n = o, i = 1;
  } finally {
    ve(r, n, i);
  }
}, isDirEmptySync(e) {
  let o = [];
  try {
    const t = _e(o, Pe`fs.isDirEmptySync(${e})`, 0);
    let r = this.readdirSync(e);
    return r.length === 0;
  } catch (n) {
    var i = n, s = 1;
  } finally {
    ve(o, i, s);
  }
}, rmdirSync(e) {
  let r = [];
  try {
    const t = _e(r, Pe`fs.rmdirSync(${e})`, 0);
    Y.rmdirSync(e);
  } catch (o) {
    var n = o, i = 1;
  } finally {
    ve(r, n, i);
  }
}, rmSync(e, t) {
  let o = [];
  try {
    const r = _e(o, Pe`fs.rmSync(${e})`, 0);
    Y.rmSync(e, t);
  } catch (n) {
    var i = n, s = 1;
  } finally {
    ve(o, i, s);
  }
}, createWriteStream(e) {
  return Y.createWriteStream(e);
}, async readFileBytes(e, t) {
  if (t === void 0) return _T(e);
  let r = await z2(e, "r");
  try {
    let { size: o } = await r.stat(), n = Math.min(o, t), i = Buffer.allocUnsafe(n), s = 0;
    while (s < n) {
      let { bytesRead: a } = await r.read(i, s, n - s, s);
      if (a === 0) break;
      s += a;
    }
    return s < n ? i.subarray(0, s) : i;
  } finally {
    await r.close();
  }
} };
var W2 = V2;
function Be() {
  return W2;
}
function K2(e, t) {
  if (e.destroyed) return;
  e.write(t);
}
function vT(e) {
  K2(process.stderr, e);
}
function Eh(e) {
  return e.charAt(0).toUpperCase() + e.slice(1);
}
var xme = typeof String.prototype.isWellFormed === "function" ? Function.prototype.call.bind(String.prototype.isWellFormed) : void 0;
var wme = typeof String.prototype.toWellFormed === "function" ? Function.prototype.call.bind(String.prototype.toWellFormed) : void 0;
var G2 = /api[_-]?key|secret|token|password|passwd|credential|bearer|authorization|auth[_-]?header|cookie|session[_-]?(?:id|key)|connection[_-]?string|(?:private|ssh|encryption|signing|access|deploy|master|license)[_-]?key|client[_-]?secret/i;
var xT = "[^\\s,;&}\\])]+";
var ET = "-----BEGIN[ A-Z0-9_-]{0,100}PRIVATE KEY(?: BLOCK)?-----[\\s\\S-]{64,}?-----END[ A-Z0-9_-]{0,100}PRIVATE KEY(?: BLOCK)?-----";
var J2 = `[^\\s-]{0,4}${ET}['"\`]?`;
var wT = `\\[REDACTED\\]|"[^"]*"|'[^']*'|(?:Bearer|Basic)\\s+(?:\\[REDACTED\\]|${xT})|${J2}|${xT}`;
var X2 = ["sk", "ant", "api"].join("-");
var Y2 = [{ id: "url-userinfo", source: ":\\/\\/([^/@\\s]+)@", confidence: "low" }, { id: "gcp-service-account", source: "\\b([a-z0-9-]+@[a-z0-9-]+\\.iam\\.gserviceaccount\\.com)\\b", flags: "i", confidence: "low" }, { id: "loose-anthropic-key", source: "\\b(sk-ant-?[\\w-]{10,})", confidence: "low" }, { id: "http-auth-scheme", source: "\\b(?:Bearer|Basic)\\s+([A-Za-z0-9+/=._~-]{20,})", flags: "i", confidence: "low" }, { id: "loose-jwt", source: "\\b(eyJ[A-Za-z0-9_-]{10,}\\.[A-Za-z0-9_-]{10,}\\.[A-Za-z0-9_-]{10,})", confidence: "low" }, { id: "sensitive-assign", source: `(?:${G2.source})[\\w.-]*["']?\\s*[=:]\\s*(${wT})`, flags: "i", confidence: "low" }, { id: "cloud-env-var", source: `\\b(?:AWS|GOOGLE|GCP|GCLOUD|AZURE)_\\w+\\s*[=:]\\s*(${wT})`, flags: "i", confidence: "low" }, { id: "aws-access-token", source: "\\b((?:A3T[A-Z0-9]|AKIA|ASIA|ABIA|ACCA)[A-Z2-7]{16})\\b", confidence: "high" }, { id: "gcp-api-key", source: `\\b(AIza[\\w-]{35})(?:[\\x60'"\\s;]|\\\\[nr]|$)`, confidence: "high" }, { id: "azure-ad-client-secret", source: `(?:^|[\\\\'"\\x60\\s>=:(,)])([a-zA-Z0-9_~.]{3}\\dQ~[a-zA-Z0-9_~.-]{31,34})(?:$|[\\\\'"\\x60\\s<),])`, confidence: "high" }, { id: "digitalocean-pat", source: `\\b(dop_v1_[a-f0-9]{64})(?:[\\x60'"\\s;]|\\\\[nr]|$)`, confidence: "high" }, { id: "digitalocean-access-token", source: `\\b(doo_v1_[a-f0-9]{64})(?:[\\x60'"\\s;]|\\\\[nr]|$)`, confidence: "high" }, { id: "anthropic-api-key", source: `\\b(${X2}03-[a-zA-Z0-9_\\-]{93}AA)(?:[\\x60'"\\s;]|\\\\[nr]|$)`, confidence: "high" }, { id: "anthropic-admin-api-key", source: `\\b(sk-ant-admin01-[a-zA-Z0-9_\\-]{93}AA)(?:[\\x60'"\\s;]|\\\\[nr]|$)`, confidence: "high" }, { id: "openai-api-key", source: `\\b(sk-(?:proj|svcacct|admin)-(?:[A-Za-z0-9_-]{74}|[A-Za-z0-9_-]{58})T3BlbkFJ(?:[A-Za-z0-9_-]{74}|[A-Za-z0-9_-]{58})\\b|sk-[a-zA-Z0-9]{20}T3BlbkFJ[a-zA-Z0-9]{20})(?:[\\x60'"\\s;]|\\\\[nr]|$)`, confidence: "high" }, { id: "huggingface-access-token", source: `\\b(hf_[a-zA-Z]{34})(?:[\\x60'"\\s;]|\\\\[nr]|$)`, confidence: "high" }, { id: "github-pat", source: "ghp_[0-9a-zA-Z]{36}", confidence: "high" }, { id: "github-fine-grained-pat", source: "github_pat_\\w{82}", confidence: "high" }, { id: "github-app-token", source: "(?:ghu|ghs)_[0-9a-zA-Z]{36}", confidence: "high" }, { id: "github-oauth", source: "gho_[0-9a-zA-Z]{36}", confidence: "high" }, { id: "github-refresh-token", source: "ghr_[0-9a-zA-Z]{36}", confidence: "high" }, { id: "gitlab-pat", source: "glpat-[\\w-]{20}", confidence: "high" }, { id: "gitlab-deploy-token", source: "gldt-[0-9a-zA-Z_\\-]{20}", confidence: "high" }, { id: "slack-bot-token", source: "xoxb-[0-9]{10,13}-[0-9]{10,13}[a-zA-Z0-9-]*", confidence: "high" }, { id: "slack-user-token", source: "xox[pe](?:-[0-9]{10,13}){3}-[a-zA-Z0-9-]{28,34}", confidence: "high" }, { id: "slack-app-token", source: "xapp-\\d-[A-Z0-9]+-\\d+-[a-z0-9]+", flags: "i", confidence: "high" }, { id: "twilio-api-key", source: "SK[0-9a-fA-F]{32}", confidence: "high" }, { id: "sendgrid-api-token", source: `\\b(SG\\.[a-zA-Z0-9=_\\-.]{66})(?:[\\x60'"\\s;]|\\\\[nr]|$)`, confidence: "high" }, { id: "npm-access-token", source: `\\b(npm_[a-zA-Z0-9]{36})(?:[\\x60'"\\s;]|\\\\[nr]|$)`, confidence: "high" }, { id: "pypi-upload-token", source: "pypi-AgEIcHlwaS5vcmc[\\w-]{50,1000}", confidence: "high" }, { id: "databricks-api-token", source: `\\b(dapi[a-f0-9]{32}(?:-\\d)?)(?:[\\x60'"\\s;]|\\\\[nr]|$)`, confidence: "high" }, { id: "hashicorp-tf-api-token", source: "[a-zA-Z0-9]{14}\\.atlasv1\\.[a-zA-Z0-9\\-_=]{60,70}", confidence: "high" }, { id: "pulumi-api-token", source: `\\b(pul-[a-f0-9]{40})(?:[\\x60'"\\s;]|\\\\[nr]|$)`, confidence: "high" }, { id: "postman-api-token", source: `\\b(PMAK-[a-fA-F0-9]{24}-[a-fA-F0-9]{34})(?:[\\x60'"\\s;]|\\\\[nr]|$)`, confidence: "high" }, { id: "grafana-api-key", source: `\\b(eyJrIjoi[A-Za-z0-9+/]{70,400}={0,3})(?:[\\x60'"\\s;]|\\\\[nr]|$)`, confidence: "high" }, { id: "grafana-cloud-api-token", source: `\\b(glc_[A-Za-z0-9+/]{32,400}={0,3})(?:[\\x60'"\\s;]|\\\\[nr]|$)`, confidence: "high" }, { id: "grafana-service-account-token", source: `\\b(glsa_[A-Za-z0-9]{32}_[A-Fa-f0-9]{8})(?:[\\x60'"\\s;]|\\\\[nr]|$)`, confidence: "high" }, { id: "sentry-user-token", source: `\\b(sntryu_[a-f0-9]{64})(?:[\\x60'"\\s;]|\\\\[nr]|$)`, confidence: "high" }, { id: "sentry-org-token", source: "\\bsntrys_eyJpYXQiO[a-zA-Z0-9+/]{10,200}(?:LCJyZWdpb25fdXJs|InJlZ2lvbl91cmwi|cmVnaW9uX3VybCI6)[a-zA-Z0-9+/]{10,200}={0,2}_[a-zA-Z0-9+/]{43}", confidence: "high" }, { id: "stripe-access-token", source: `\\b((?:sk|rk)_(?:test|live|prod)_[a-zA-Z0-9]{10,99})(?:[\\x60'"\\s;]|\\\\[nr]|$)`, confidence: "high" }, { id: "shopify-access-token", source: "shpat_[a-fA-F0-9]{32}", confidence: "high" }, { id: "shopify-shared-secret", source: "shpss_[a-fA-F0-9]{32}", confidence: "high" }, { id: "private-key", source: ET, flags: "i", confidence: "high" }];
var kT = null;
function Q2(e) {
  return Y2.map((t) => ({ id: t.id, confidence: t.confidence, re: new RegExp(t.source, e ? (t.flags ?? "").replace("g", "") + "g" : t.flags ?? "") }));
}
function TT(e) {
  kT ??= Q2(true);
  for (let t of kT) e = e.replace(t.re, (r, o) => {
    if (typeof o !== "string") return "[REDACTED]";
    let n = o.length >= 2 && (o[0] === '"' || o[0] === "'") && o.at(-1) === o[0] ? o[0] : "", i = r.lastIndexOf(o);
    return `${r.slice(0, i)}${n}[REDACTED]${n}${r.slice(i + o.length)}`;
  });
  return e;
}
var $h = { verbose: 0, debug: 1, info: 2, warn: 3, error: 4 };
var oB = Ce(() => {
  let e = process.env.CLAUDE_CODE_DEBUG_LOG_LEVEL?.toLowerCase().trim();
  if (e && Object.hasOwn($h, e)) return e;
  return "debug";
});
var iB = false;
function bd() {
  if (typeof process > "u" || !Array.isArray(process.argv)) return [];
  let e = process.argv.indexOf("--");
  return e === -1 ? process.argv : process.argv.slice(0, e);
}
var Oh = Ce(() => {
  let e = bd();
  return iB || Ee(process.env.DEBUG) || Ee(process.env.DEBUG_SDK) || e.includes("--debug") || e.includes("-d") || OT() || e.some((t) => t.startsWith("--debug=")) || AT() !== null;
});
var sB = Ce(() => {
  let e = bd().find((r) => r.startsWith("--debug="));
  if (!e) return null;
  let t = e.substring(8);
  return yT(t);
});
var OT = Ce(() => {
  let e = bd();
  return e.includes("--debug-to-stderr") || e.includes("-d2e");
});
var AT = Ce(() => {
  let e = bd();
  for (let t = 0; t < e.length; t++) {
    let r = e[t];
    if (r.startsWith("--debug-file=")) return RT(r.substring(13));
    if (r === "--debug-file" && t + 1 < e.length) return RT(e[t + 1]);
  }
  return null;
});
function RT(e) {
  return xw(e) ? null : nB(e);
}
function aB(e) {
  if (!Oh()) return false;
  if (typeof process > "u" || typeof process.versions > "u" || typeof process.versions.node > "u") return false;
  let t = sB();
  return bT(e, t);
}
var cB = false;
var lB = 10485760;
var yd = null;
var Ph = Promise.resolve();
var qa = -1;
var Ih = false;
var Ah = null;
async function CT(e, t, r = lB) {
  if (qa < 0) qa = await tB(e).then((o) => o.size).catch(() => 0);
  else qa += t;
  if (qa <= r || Ih) return;
  Ih = true;
  try {
    let o = e.endsWith(".txt") ? `${e.slice(0, -4)}.1.txt` : `${e}.1`;
    try {
      await IT(e, o);
    } catch (n) {
      if (!zr(n)) await Rh(o).catch(() => {
      }), await IT(e, o).catch(() => Rh(e).catch(() => {
      }));
    }
    qa = 0;
  } finally {
    Ih = false;
  }
}
function MT(e) {
  return Ah = Mh(e, `${kh()}.txt`), Ah;
}
async function uB(e, t, r, o) {
  if (e) await eB(t, { recursive: true }).catch(() => {
  });
  let n = r;
  try {
    await PT(r, o);
  } catch (i) {
    if (!mh(i)) throw i;
    n = MT(r), await PT(n, o);
  }
  await CT(n, Buffer.byteLength(o)).catch(Ch), NT();
}
function Ch() {
}
function dB() {
  if (!yd) {
    let e = null;
    yd = mT({ writeFn: (t) => {
      let r = DT(), o = $T(r), n = e !== o;
      if (e = o, Oh()) {
        if (n) try {
          Be().mkdirSync(o);
        } catch {
        }
        let i = r;
        try {
          Be().appendFileSync(r, t);
        } catch (s) {
          if (!mh(s)) throw s;
          i = MT(r), Be().appendFileSync(i, t);
        }
        CT(i, Buffer.byteLength(t)).catch(Ch), NT();
        return;
      }
      Ph = Ph.then(uB.bind(null, n, o, r, t)).catch(Ch);
    }, flushIntervalMs: 1e3, maxBufferSize: 100, immediateMode: Oh() }), hT(async () => {
      yd?.dispose(), await Ph;
    });
  }
  return yd;
}
function ee(e, { level: t } = { level: "debug" }) {
  if ($h[t] < $h[oB()]) return;
  if (!aB(e)) return;
  if (cB && e.includes(`
`)) e = pe(e);
  let o = `${(/* @__PURE__ */ new Date()).toISOString()} [${t.toUpperCase()}] ${TT(e.trim())}
`;
  if (OT()) {
    vT(o);
    return;
  }
  dB().write(o);
}
function DT() {
  return AT() ?? Ah ?? process.env.CLAUDE_CODE_DEBUG_LOGS_DIR ?? Mh(qt(), "debug", `${kh()}.txt`);
}
var NT = Ce(async () => {
  try {
    let e = DT(), t = $T(e), r = Mh(t, "latest");
    await Rh(r).catch(() => {
    }), await rB(e, r);
  } catch {
  }
});
var Vme = (() => {
  let e = process.env.CLAUDE_CODE_SLOW_OPERATION_THRESHOLD_MS;
  if (e !== void 0) {
    let t = Number(e);
    if (!Number.isNaN(t) && t >= 0) return t;
  }
  return 1 / 0;
})();
var pB = { [Symbol.dispose]() {
} };
function fB() {
  return pB;
}
var Pe = fB;
function pe(e, t, r) {
  let n = [];
  try {
    const o = _e(n, Pe`JSON.stringify(${e})`, 0);
    return JSON.stringify(e, t, r);
  } catch (i) {
    var s = i, a = 1;
  } finally {
    ve(n, s, a);
  }
}
var Ze = (e, t) => {
  let o = [];
  try {
    const r = _e(o, Pe`JSON.parse(${e})`, 0);
    return typeof t > "u" ? JSON.parse(e) : JSON.parse(e, t);
  } catch (n) {
    var i = n, s = 1;
  } finally {
    ve(o, i, s);
  }
};
function mB(e) {
  let t = e.trim();
  return t.startsWith("{") && t.endsWith("}");
}
function UT(e, t) {
  let r = { ...e };
  if (t) {
    let o = t.enabled === true && t.failIfUnavailable === void 0 ? { ...t, failIfUnavailable: true } : t, n = r.settings;
    if (n && !mB(n)) throw Error("Cannot use both a settings file path and the sandbox option. Include the sandbox configuration in your settings file instead.");
    let i = { sandbox: o };
    if (n) try {
      i = { ...Ze(n), sandbox: o };
    } catch {
    }
    r.settings = pe(i);
  }
  return r;
}
var bB = 2e3;
var _d = /* @__PURE__ */ new Set();
var zT = false;
function _B() {
  for (let e of _d) if (!e.killed) if (process.platform === "win32") try {
    e.stdin.end();
  } catch {
  }
  else e.kill("SIGTERM");
}
function vB(e) {
  if (_d.add(e), !zT) zT = true, process.on("exit", _B);
}
var Dh = class {
  options;
  process;
  processStdin;
  processStdout;
  ready = false;
  abortController;
  exitError;
  exitListeners = [];
  abortHandler;
  forwardedAbort = qs();
  pendingWrites = [];
  pendingEndInput = false;
  spawnResolve;
  spawnReject;
  spawnPromise;
  constructor(e) {
    this.options = e;
    if (this.abortController = e.abortController || qs(), e.deferSpawn) this.spawnPromise = new Promise((t, r) => {
      this.spawnResolve = t, this.spawnReject = r;
    }), this.spawnPromise.catch(() => {
    });
    else this.initialize();
  }
  spawn() {
    try {
      this.initialize();
    } catch (t) {
      throw this.spawnAbort(kr(t)), t;
    }
    let e = this.pendingWrites;
    if (this.pendingWrites = [], this.spawnResolve) this.spawnResolve(), this.spawnResolve = void 0, this.spawnReject = void 0;
    for (let t of e) this.write(t);
    if (this.pendingEndInput) this.pendingEndInput = false, this.processStdin?.end();
  }
  spawnAbort(e) {
    if (this.spawnReject) this.spawnReject(e), this.spawnReject = void 0, this.spawnResolve = void 0, this.pendingWrites = [];
  }
  updateEnv(e) {
    if (this.options.env) Object.assign(this.options.env, e);
    else this.options.env = { ...e };
  }
  updateResume(e) {
    this.options.resume = e;
  }
  getDefaultExecutable() {
    return _u() ? "bun" : "node";
  }
  spawnLocalProcess(e) {
    let { command: t, args: r, cwd: o, env: n, signal: i } = e, s = Ee(n.DEBUG_CLAUDE_AGENT_SDK) || this.options.stderr ? "pipe" : "ignore", a = gB(t, r, { cwd: o, stdio: ["pipe", "pipe", s], signal: i, env: n, windowsHide: true });
    if (Ee(n.DEBUG_CLAUDE_AGENT_SDK) || this.options.stderr) a.stderr.on("data", (u) => {
      let d = u.toString();
      if (vt(d), this.options.stderr) this.options.stderr(d);
    });
    return { stdin: a.stdin, stdout: a.stdout, get killed() {
      return a.killed;
    }, get exitCode() {
      return a.exitCode;
    }, kill: a.kill.bind(a), on: a.on.bind(a), once: a.once.bind(a), off: a.off.bind(a) };
  }
  initialize() {
    try {
      let { additionalDirectories: e = [], agent: t, betas: r, cwd: o, executable: n = this.getDefaultExecutable(), executableArgs: i = [], extraArgs: s = {}, pathToClaudeCodeExecutable: a, env: c = { ...process.env }, thinkingConfig: u, maxTurns: d, maxBudgetUsd: p, taskBudget: f, model: m, fallbackModel: g, jsonSchema: h, permissionMode: y, allowDangerouslySkipPermissions: v, permissionPromptToolName: x, continueConversation: w, resume: A, settingSources: U, skills: se, disallowedTools: Le = [], tools: Ye, mcpServers: Lt, strictMcpConfig: yt, canUseTool: Qn, includePartialMessages: Go, plugins: Rr, sandbox: js } = this.options, { allowedTools: cn = [] } = this.options;
      if (se !== void 0) {
        let je = se === "all" ? ["Skill"] : se.map((Sr) => `Skill(${Sr})`), Ft = new Set(cn);
        cn = [...cn, ...je.filter((Sr) => !Ft.has(Sr))];
      }
      let V = ["--output-format", "stream-json", "--verbose", "--input-format", "stream-json"];
      if (u) {
        switch (u.type) {
          case "enabled":
            if (u.budgetTokens === void 0) V.push("--thinking", "adaptive");
            else V.push("--max-thinking-tokens", u.budgetTokens.toString());
            break;
          case "disabled":
            V.push("--thinking", "disabled");
            break;
          case "adaptive":
            V.push("--thinking", "adaptive");
            break;
        }
        if (u.type !== "disabled" && u.display) V.push("--thinking-display", u.display);
      }
      if (this.options.effort) V.push("--effort", this.options.effort);
      if (d) V.push("--max-turns", d.toString());
      if (p !== void 0) V.push("--max-budget-usd", p.toString());
      if (f) V.push("--task-budget", f.total.toString());
      if (m) V.push("--model", m);
      if (t) V.push("--agent", t);
      if (r && r.length > 0) V.push("--betas", r.join(","));
      if (h) V.push("--json-schema", pe(h));
      if (this.options.debugFile) V.push("--debug-file", this.options.debugFile);
      else if (this.options.debug) V.push("--debug");
      if (!this.options.debugFile && !this.options.spawnClaudeCodeProcess) {
        let je = dE();
        if (je) V.push("--debug-file", je);
      }
      if (Qn) {
        if (x) throw Error("canUseTool callback cannot be used with permissionPromptToolName. Please use one or the other.");
        V.push("--permission-prompt-tool", "stdio");
      } else if (x) V.push("--permission-prompt-tool", x);
      if (w) V.push("--continue");
      if (A) V.push("--resume", A);
      if (this.options.assistant) V.push("--assistant");
      if (this.options.channels && this.options.channels.length > 0) V.push("--channels", ...this.options.channels);
      if (cn.length > 0) V.push("--allowedTools", cn.join(","));
      if (Le.length > 0) V.push("--disallowedTools", Le.join(","));
      if (Ye !== void 0) if (Array.isArray(Ye)) if (Ye.length === 0) V.push("--tools", "");
      else V.push("--tools", Ye.join(","));
      else V.push("--tools", "default");
      if (Lt && Object.keys(Lt).length > 0) V.push("--mcp-config", pe({ mcpServers: Lt }));
      if (U !== void 0) V.push(`--setting-sources=${U.join(",")}`);
      if (yt) V.push("--strict-mcp-config");
      if (y) V.push("--permission-mode", y);
      if (v) V.push("--allow-dangerously-skip-permissions");
      if (g) {
        if (m && g === m) throw Error("Fallback model cannot be the same as the main model. Please specify a different model for fallbackModel option.");
        V.push("--fallback-model", g);
      }
      if (this.options.includeHookEvents) V.push("--include-hook-events");
      if (Go) V.push("--include-partial-messages");
      if (this.options.sessionMirror) V.push("--session-mirror");
      for (let je of e) V.push("--add-dir", je);
      if (Rr && Rr.length > 0) for (let je of Rr) if (je.type === "local") V.push(je.skipMcpDiscovery ? "--plugin-dir-no-mcp" : "--plugin-dir", je.path);
      else throw Error(`Unsupported plugin type: ${je.type}`);
      if (this.options.forkSession) V.push("--fork-session");
      if (this.options.resumeSessionAt) V.push("--resume-session-at", this.options.resumeSessionAt);
      if (this.options.sessionId) V.push("--session-id", this.options.sessionId);
      if (this.options.persistSession === false) V.push("--no-session-persistence");
      if (this.options.managedSettings) V.push("--managed-settings", this.options.managedSettings);
      let mu = { ...s ?? {} };
      if (this.options.settings) mu.settings = this.options.settings;
      let gu = UT(mu, js);
      for (let [je, Ft] of Object.entries(gu)) if (Ft === null) V.push(`--${je}`);
      else V.push(`--${je}`, Ft);
      if (!c.CLAUDE_CODE_ENTRYPOINT) c.CLAUDE_CODE_ENTRYPOINT = "sdk-ts";
      if (delete c.NODE_OPTIONS, Ee(c.DEBUG_CLAUDE_AGENT_SDK)) c.DEBUG = "1";
      else delete c.DEBUG;
      let Us = SB(a), zs = Us ? a : n, Ls = Us ? [...i, ...V] : [...i, a, ...V], hu = { command: zs, args: Ls, cwd: o, env: c, signal: this.forwardedAbort.signal };
      if (this.options.spawnClaudeCodeProcess) vt(`Spawning Claude Code (custom): ${zs} ${Ls.join(" ")}`), this.process = this.options.spawnClaudeCodeProcess(hu);
      else vt(`Spawning Claude Code: ${zs} ${Ls.join(" ")}`), this.process = this.spawnLocalProcess(hu);
      if (this.processStdin = this.process.stdin, this.processStdout = this.process.stdout, vB(this.process), this.abortHandler = () => this.close(), this.abortController.signal.addEventListener("abort", this.abortHandler), this.abortController.signal.aborted) this.close();
      this.process.on("error", (je) => {
        if (this.ready = false, this.abortController.signal.aborted) this.exitError = new ot("Claude Code process aborted by user");
        else if (rE(je)) {
          let Ft = xB(a, Us);
          this.exitError = ReferenceError(Ft), vt(this.exitError.message);
        } else this.exitError = Error(`Failed to spawn Claude Code process: ${je.message}`), vt(this.exitError.message);
      }), this.process.on("exit", (je, Ft) => {
        if (this.ready = false, this.abortController.signal.aborted) this.exitError = new ot("Claude Code process aborted by user");
        else {
          let Sr = this.getProcessExitError(je, Ft);
          if (Sr) this.exitError = Sr, vt(Sr.message);
        }
      }), this.ready = !this.abortController.signal.aborted;
    } catch (e) {
      throw this.ready = false, e;
    }
  }
  getProcessExitError(e, t) {
    if (e !== 0 && e !== null) return Error(`Claude Code process exited with code ${e}`);
    else if (t) return Error(`Claude Code process terminated by signal ${t}`);
    return;
  }
  write(e) {
    if (this.abortController.signal.aborted) throw new ot("Operation aborted");
    if (this.spawnResolve) {
      this.pendingWrites.push(e);
      return;
    }
    if (!this.ready || !this.processStdin) throw Error("ProcessTransport is not ready for writing");
    if (this.processStdin.writableEnded) {
      vt("[ProcessTransport] Dropping write to ended stdin stream");
      return;
    }
    if (this.process?.killed || this.process?.exitCode !== null) throw Error("Cannot write to terminated process");
    if (this.exitError) throw Error(`Cannot write to process that exited with error: ${this.exitError.message}`);
    vt(`[ProcessTransport] Writing to stdin: ${e.substring(0, 100)}`);
    try {
      if (!this.processStdin.write(e)) vt("[ProcessTransport] Write buffer full, data queued");
    } catch (t) {
      throw this.ready = false, Error(`Failed to write to process stdin: ${bi(t)}`);
    }
  }
  [Symbol.dispose]() {
    this.close();
  }
  close() {
    if (this.spawnAbort(this.abortController.signal.aborted ? new ot("Claude Code process aborted by user") : Error("Query closed before spawn")), this.processStdin) this.processStdin.end(), this.processStdin = void 0;
    if (this.abortHandler) this.abortController.signal.removeEventListener("abort", this.abortHandler), this.abortHandler = void 0;
    for (let { handler: r } of this.exitListeners) this.process?.off("exit", r);
    this.exitListeners = [];
    let e = () => {
      if (this.abortController.signal.aborted) this.forwardedAbort.abort(this.abortController.signal.reason);
    }, t = this.process;
    if (t && !t.killed && t.exitCode === null) setTimeout((r, o) => {
      if (r.exitCode !== null) {
        o();
        return;
      }
      if (process.platform === "win32") {
        setTimeout((n, i) => {
          if (n.exitCode === null) n.kill("SIGKILL");
          i();
        }, 5e3, r, o).unref();
        return;
      }
      r.kill("SIGTERM"), setTimeout((n) => {
        if (n.exitCode === null) n.kill("SIGKILL");
      }, 5e3, r).unref(), o();
    }, bB, t, e).unref(), t.once("exit", () => _d.delete(t));
    else if (t) _d.delete(t), e();
    this.ready = false;
  }
  isReady() {
    return this.ready;
  }
  async *readMessages() {
    if (this.spawnPromise) await this.spawnPromise, this.spawnPromise = void 0;
    if (!this.processStdout) throw Error("ProcessTransport output stream not available");
    if (this.exitError) throw this.exitError;
    let e = yB({ input: this.processStdout }), t = this.process ? (() => {
      let r = this.process, o = () => e.close();
      return r.on("error", o), () => r.off("error", o);
    })() : void 0;
    if (this.exitError) e.close();
    try {
      for await (let r of e) if (r.trim()) {
        let o;
        try {
          o = Ze(r);
        } catch (n) {
          vt(`Non-JSON stdout: ${r}`);
          continue;
        }
        yield o;
      }
      if (this.exitError) throw this.exitError;
      await this.waitForExit();
    } catch (r) {
      throw r;
    } finally {
      t?.(), e.close();
    }
  }
  endInput() {
    if (this.spawnResolve) {
      this.pendingEndInput = true;
      return;
    }
    if (this.processStdin) this.processStdin.end();
  }
  getInputStream() {
    return this.processStdin;
  }
  onExit(e) {
    if (!this.process) return () => {
    };
    let t = (r, o) => {
      let n = this.getProcessExitError(r, o);
      e(n);
    };
    return this.process.on("exit", t), this.exitListeners.push({ callback: e, handler: t }), () => {
      if (this.process) this.process.off("exit", t);
      let r = this.exitListeners.findIndex((o) => o.handler === t);
      if (r !== -1) this.exitListeners.splice(r, 1);
    };
  }
  async waitForExit() {
    if (!this.process) {
      if (this.exitError) throw this.exitError;
      return;
    }
    if (this.process.exitCode !== null || this.process.killed || this.exitError) {
      if (this.exitError) throw this.exitError;
      return;
    }
    return new Promise((e, t) => {
      let r = (n, i) => {
        if (this.abortController.signal.aborted) {
          t(new ot("Operation aborted"));
          return;
        }
        let s = this.getProcessExitError(n, i);
        if (s) t(s);
        else e();
      };
      this.process.once("exit", r);
      let o = (n) => {
        this.process.off("exit", r), t(n);
      };
      this.process.once("error", o), this.process.once("exit", () => {
        this.process.off("error", o);
      });
    });
  }
};
function SB(e) {
  return ![".js", ".mjs", ".tsx", ".ts", ".jsx"].some((r) => e.endsWith(r));
}
function xB(e, t) {
  if (hB(e)) return t ? `Claude Code native binary at ${e} exists but failed to launch. This usually means the binary does not match this system's libc \u2014 e.g. spawning a musl-linked binary on a glibc Linux host fails because the musl dynamic loader (/lib/ld-musl-*) is missing. Specify a matching binary with options.pathToClaudeCodeExecutable.` : `Claude Code executable at ${e} exists but failed to launch.`;
  return t ? `Claude Code native binary not found at ${e}. Please ensure Claude Code is installed via native installer or specify a valid path with options.pathToClaudeCodeExecutable.` : `Claude Code executable not found at ${e}. Is options.pathToClaudeCodeExecutable set?`;
}
var Ii = "@anthropic-ai/claude-agent-sdk";
function kB() {
  if (process.platform !== "linux") return false;
  let e = typeof process.report?.getReport === "function" ? process.report.getReport() : null;
  return e != null && e.header?.glibcVersionRuntime === void 0;
}
function LT(e, t = process.platform, r = process.arch, o = wB, n = kB()) {
  let s = t === "win32" ? ".exe" : "", c = (t === "android" ? [`${Ii}-linux-${r}-android`] : t === "linux" ? n ? [`${Ii}-linux-${r}-musl`, `${Ii}-linux-${r}`] : [`${Ii}-linux-${r}`, `${Ii}-linux-${r}-musl`] : [`${Ii}-${t}-${r}`]).map((u) => `${u}/claude${s}`);
  for (let u of c) try {
    let d = e(u);
    if (o(d)) return d;
  } catch {
  }
  return null;
}
var Za = class {
  returned;
  queue = [];
  readResolve;
  readReject;
  isDone = false;
  hasError;
  started = false;
  constructor(e) {
    this.returned = e;
  }
  [Symbol.asyncIterator]() {
    if (this.started) throw Error("Stream can only be iterated once");
    return this.started = true, this;
  }
  next() {
    if (this.queue.length > 0) return Promise.resolve({ done: false, value: this.queue.shift() });
    if (this.isDone) return Promise.resolve({ done: true, value: void 0 });
    if (this.hasError) return Promise.reject(this.hasError);
    return new Promise((e, t) => {
      this.readResolve = e, this.readReject = t;
    });
  }
  enqueue(e) {
    if (this.readResolve) {
      let t = this.readResolve;
      this.readResolve = void 0, this.readReject = void 0, t({ done: false, value: e });
    } else this.queue.push(e);
  }
  done() {
    if (this.isDone = true, this.readResolve) {
      let e = this.readResolve;
      this.readResolve = void 0, this.readReject = void 0, e({ done: true, value: void 0 });
    }
  }
  error(e) {
    if (this.hasError = e, this.readReject) {
      let t = this.readReject;
      this.readResolve = void 0, this.readReject = void 0, t(e);
    }
  }
  return() {
    if (this.isDone = true, this.returned) this.returned();
    return Promise.resolve({ done: true, value: void 0 });
  }
};
function EB() {
  return { eventQueue: [], sink: null };
}
var TB = EB();
function Ri(e, t) {
  let r = TB;
  if (r.sink === null) {
    r.eventQueue.push({ eventName: e, metadata: t, async: false });
    return;
  }
  r.sink.logEvent(e, t);
}
function Nh(e, t) {
  Ri("tengu_feature_ok", { feature_name: yi(e), ...t });
}
function jh(e, t, r) {
  Ri("tengu_feature_bad", { ...r, feature_name: yi(e), error_code: t });
}
async function sr(e, t, r) {
  try {
    let o = await t();
    return Nh(e), o;
  } catch (o) {
    throw jh(e, r?.(o) ?? "error"), o;
  }
}
var Uh = class {
  sendMcpMessage;
  isClosed = false;
  constructor(e) {
    this.sendMcpMessage = e;
  }
  onclose;
  onerror;
  onmessage;
  async start() {
  }
  async send(e) {
    if (this.isClosed) throw Error("Transport is closed");
    this.sendMcpMessage(e);
  }
  async close() {
    if (this.isClosed) return;
    this.isClosed = true, this.onclose?.();
  }
};
var BT = Symbol("suppressControlResponse");
var zh = class {
  transport;
  isSingleUserTurn;
  canUseTool;
  hooks;
  abortController;
  jsonSchema;
  initConfig;
  onElicitation;
  getOAuthToken;
  getHostAuthToken;
  onUserDialog;
  pendingControlResponses = /* @__PURE__ */ new Map();
  cleanupPerformed = false;
  sdkMessages;
  inputStream = new Za();
  initialization;
  cancelControllers = /* @__PURE__ */ new Map();
  hookCallbacks = /* @__PURE__ */ new Map();
  nextCallbackId = 0;
  sdkMcpTransports = /* @__PURE__ */ new Map();
  sdkMcpServerInstances = /* @__PURE__ */ new Map();
  pendingMcpResponses = /* @__PURE__ */ new Map();
  firstResultReceivedResolve;
  firstResultReceived = false;
  lastErrorResultText;
  transcriptMirrorBatcher;
  cleanupCallbacks = [];
  cleanupPromise;
  setIsSingleUserTurn(e) {
    this.isSingleUserTurn = e;
  }
  setTranscriptMirrorBatcher(e) {
    this.transcriptMirrorBatcher = e;
  }
  reportMirrorError(e, t) {
    let r = { type: "system", subtype: "mirror_error", error: t, key: e, uuid: Ha(), session_id: e.sessionId };
    this.inputStream.enqueue(r);
  }
  addCleanupCallback(e) {
    if (this.cleanupPerformed) e();
    else this.cleanupCallbacks.push(e);
  }
  isClosed() {
    return this.cleanupPerformed;
  }
  hasBidirectionalNeeds() {
    return this.sdkMcpTransports.size > 0 || this.hooks !== void 0 && Object.keys(this.hooks).length > 0 || this.canUseTool !== void 0 || this.onElicitation !== void 0 || this.onUserDialog !== void 0 || this.getOAuthToken !== void 0 || this.getHostAuthToken !== void 0;
  }
  constructor(e, t, r, o, n, i = /* @__PURE__ */ new Map(), s, a, c, u, d, p) {
    this.transport = e;
    this.isSingleUserTurn = t;
    this.canUseTool = r;
    this.hooks = o;
    this.abortController = n;
    this.jsonSchema = s;
    this.initConfig = a;
    this.onElicitation = c;
    this.getOAuthToken = u;
    this.getHostAuthToken = d;
    this.onUserDialog = p;
    for (let [f, m] of i) this.connectSdkMcpServer(f, m);
    this.sdkMessages = this.readSdkMessages(), this.readMessages(), this.initialization = this.initialize(), this.initialization.catch(() => {
    });
  }
  setError(e) {
    this.inputStream.error(e);
  }
  async stopTask(e) {
    await this.request({ subtype: "stop_task", task_id: e });
  }
  async backgroundTasks(e) {
    return (await this.request({ subtype: "background_tasks", tool_use_id: e })).response.backgrounded ?? true;
  }
  close() {
    this.cleanup();
  }
  cleanup(e) {
    if (this.cleanupPromise) return this.cleanupPromise;
    return this.cleanupPerformed = true, this.cleanupPromise = this.performCleanup(e), this.cleanupPromise;
  }
  async performCleanup(e) {
    for (let t of this.cleanupCallbacks) try {
      t();
    } catch {
    }
    if (this.cleanupCallbacks = [], this.transcriptMirrorBatcher) try {
      await this.transcriptMirrorBatcher.flush();
    } catch {
    }
    try {
      for (let r of this.cancelControllers.values()) r.abort();
      this.cancelControllers.clear(), this.transport.close();
      let t = e ?? Error("Query closed before response received");
      for (let { reject: r } of this.pendingControlResponses.values()) r(t);
      this.pendingControlResponses.clear();
      for (let { reject: r } of this.pendingMcpResponses.values()) r(t);
      this.pendingMcpResponses.clear(), this.hookCallbacks.clear();
      for (let r of this.sdkMcpTransports.values()) r.close().catch(() => {
      });
      if (this.sdkMcpTransports.clear(), e) this.inputStream.error(e);
      else this.inputStream.done();
    } catch (t) {
    }
  }
  next(...[e]) {
    return this.sdkMessages.next(...[e]);
  }
  async return(e) {
    return await this.cleanup(), this.sdkMessages.return(e);
  }
  async throw(e) {
    return await this.cleanup(), this.sdkMessages.throw(e);
  }
  [Symbol.asyncIterator]() {
    return this.sdkMessages;
  }
  async [Symbol.asyncDispose]() {
    await this.cleanup();
  }
  async readMessages() {
    try {
      for await (let e of this.transport.readMessages()) {
        if (e.type === "control_response") {
          let t = this.pendingControlResponses.get(e.response.request_id);
          if (t) t.handler(e.response);
          continue;
        } else if (e.type === "control_request") {
          this.handleControlRequest(e);
          continue;
        } else if (e.type === "control_cancel_request") {
          this.handleControlCancelRequest(e);
          continue;
        } else if (e.type === "keep_alive") continue;
        else if (e.type === "transcript_mirror") {
          this.transcriptMirrorBatcher?.enqueue(e.filePath, e.entries);
          continue;
        }
        if (e.type === "system" && (e.subtype === "post_turn_summary" || e.subtype === "task_summary")) {
          this.inputStream.enqueue(e);
          continue;
        }
        if (e.type === "result") {
          if (this.transcriptMirrorBatcher) await this.transcriptMirrorBatcher.flush();
          if (this.lastErrorResultText = e.is_error ? e.subtype === "success" ? e.result : e.errors.join("; ") : void 0, this.firstResultReceived = true, this.firstResultReceivedResolve) this.firstResultReceivedResolve();
          if (this.isSingleUserTurn) ee("[Query.readMessages] First result received for single-turn query, closing stdin"), this.transport.endInput();
        } else if (!(e.type === "system" && e.subtype === "session_state_changed")) this.lastErrorResultText = void 0;
        this.inputStream.enqueue(e);
      }
      if (this.transcriptMirrorBatcher) await this.transcriptMirrorBatcher.flush();
      if (this.firstResultReceivedResolve) this.firstResultReceivedResolve();
      this.inputStream.done(), this.cleanup();
    } catch (e) {
      if (this.transcriptMirrorBatcher) await this.transcriptMirrorBatcher.flush();
      if (this.firstResultReceivedResolve) this.firstResultReceivedResolve();
      if (this.lastErrorResultText !== void 0 && !(e instanceof ot)) {
        let t = Error(`Claude Code returned an error result: ${this.lastErrorResultText}`);
        ee(`[Query.readMessages] Replacing exit error with result text. Original: ${bi(e)}`), this.inputStream.error(t), this.cleanup(t);
        return;
      }
      this.inputStream.error(e), this.cleanup(e);
    }
  }
  async handleControlRequest(e) {
    if (this.cancelControllers.has(e.request_id)) {
      ee(`[Query.handleControlRequest] Duplicate delivery of in-flight request ${e.request_id} (${e.request.subtype}) \u2014 skipping`);
      return;
    }
    let t = new AbortController();
    this.cancelControllers.set(e.request_id, t);
    try {
      let r = await this.processControlRequest(e, t.signal);
      if (this.cleanupPerformed) return;
      if (r === BT) return;
      let o = { type: "control_response", response: { subtype: "success", request_id: e.request_id, response: r } };
      await Promise.resolve(this.transport.write(pe(o) + `
`));
    } catch (r) {
      if (this.cleanupPerformed) return;
      let o = { type: "control_response", response: { subtype: "error", request_id: e.request_id, error: bi(r) } };
      try {
        await Promise.resolve(this.transport.write(pe(o) + `
`));
      } catch (n) {
        ee(`[Query.handleControlRequest] Error-response write failed: ${bi(n)}`, { level: "error" });
      }
    } finally {
      this.cancelControllers.delete(e.request_id);
    }
  }
  handleControlCancelRequest(e) {
    let t = this.cancelControllers.get(e.request_id);
    if (t) t.abort(), this.cancelControllers.delete(e.request_id);
  }
  async processControlRequest(e, t) {
    if (e.request.subtype === "can_use_tool") {
      if (!this.canUseTool) throw Error("canUseTool callback is not provided.");
      return { ...await this.canUseTool(e.request.tool_name, e.request.input, { signal: t, suggestions: e.request.permission_suggestions, blockedPath: e.request.blocked_path, decisionReason: e.request.decision_reason, title: e.request.title, displayName: e.request.display_name, description: e.request.description, toolUseID: e.request.tool_use_id, agentID: e.request.agent_id }), toolUseID: e.request.tool_use_id };
    } else if (e.request.subtype === "hook_callback") return await this.handleHookCallbacks(e.request.callback_id, e.request.input, e.request.tool_use_id, t);
    else if (e.request.subtype === "mcp_message") {
      let r = e.request, o = this.sdkMcpTransports.get(r.server_name);
      if (!o) throw Error(`SDK MCP server not found: ${r.server_name}`);
      if ("method" in r.message && "id" in r.message && r.message.id !== null) return { mcp_response: await this.handleMcpControlRequest(r.server_name, r, o) };
      else {
        if (o.onmessage) o.onmessage(r.message);
        return { mcp_response: { jsonrpc: "2.0", result: {}, id: 0 } };
      }
    } else if (e.request.subtype === "elicitation") {
      let r = e.request;
      if (this.onElicitation) return await this.onElicitation({ serverName: r.mcp_server_name, message: r.message, mode: r.mode, url: r.url, elicitationId: r.elicitation_id, requestedSchema: r.requested_schema, title: r.title, displayName: r.display_name, description: r.description }, { signal: t });
      return { action: "decline" };
    } else if (e.request.subtype === "request_user_dialog") {
      if (this.onUserDialog) return await this.onUserDialog({ dialogKind: e.request.dialog_kind, payload: e.request.payload, toolUseID: e.request.tool_use_id }, { signal: t });
      return ee(`[Query] No onUserDialog handler for request_user_dialog (kind=${e.request.dialog_kind}) \u2014 staying silent so a capable client (or the worker's park deadline) settles it`), Ri("tengu_request_user_dialog_response_ignored", { shape: yi("auto_cancel") }), BT;
    } else if (e.request.subtype === "oauth_token_refresh") {
      if (!this.getOAuthToken) throw Error("getOAuthToken callback is not provided.");
      return { accessToken: await this.getOAuthToken({ signal: t }) ?? null };
    } else if (e.request.subtype === "host_auth_token_refresh") {
      if (!this.getHostAuthToken) throw Error("getHostAuthToken callback is not provided.");
      return { authToken: await this.getHostAuthToken({ signal: t }) ?? null };
    }
    throw Error("Unsupported control request subtype: " + e.request.subtype);
  }
  async *readSdkMessages() {
    try {
      for await (let e of this.inputStream) yield e;
    } finally {
      await this.cleanup();
    }
  }
  async initialize() {
    let e;
    if (this.hooks) {
      e = {};
      for (let [n, i] of Object.entries(this.hooks)) if (i.length > 0) e[n] = i.map((s) => {
        let a = [];
        for (let c of s.hooks) {
          let u = `hook_${this.nextCallbackId++}`;
          this.hookCallbacks.set(u, c), a.push(u);
        }
        return { matcher: s.matcher, hookCallbackIds: a, timeout: s.timeout };
      });
    }
    let t = this.sdkMcpTransports.size > 0 ? Array.from(this.sdkMcpTransports.keys()) : void 0, r = { subtype: "initialize", hooks: e, sdkMcpServers: t, jsonSchema: this.jsonSchema, systemPrompt: typeof this.initConfig?.systemPrompt === "string" ? [this.initConfig.systemPrompt] : this.initConfig?.systemPrompt, appendSystemPrompt: this.initConfig?.appendSystemPrompt, planModeInstructions: this.initConfig?.planModeInstructions, appendSubagentSystemPrompt: this.initConfig?.appendSubagentSystemPrompt, toolAliases: this.initConfig?.toolAliases, excludeDynamicSections: this.initConfig?.excludeDynamicSections, agents: this.initConfig?.agents, title: this.initConfig?.title, skills: Array.isArray(this.initConfig?.skills) ? this.initConfig.skills : void 0, webSearchIsolationExemptMcpServers: this.initConfig?.webSearchIsolationExemptMcpServers, promptSuggestions: this.initConfig?.promptSuggestions, agentProgressSummaries: this.initConfig?.agentProgressSummaries, forwardSubagentText: this.initConfig?.forwardSubagentText, supportedDialogKinds: this.initConfig?.supportedDialogKinds };
    return (await this.request(r)).response;
  }
  async interrupt() {
    return sr("sdk_interrupt", async () => {
      await this.request({ subtype: "interrupt" });
    });
  }
  async setPermissionMode(e) {
    await this.request({ subtype: "set_permission_mode", mode: e });
  }
  async setModel(e) {
    await this.request({ subtype: "set_model", model: e });
  }
  async setMaxThinkingTokens(e, t) {
    await this.request({ subtype: "set_max_thinking_tokens", max_thinking_tokens: e, thinking_display: t });
  }
  async applyFlagSettings(e) {
    return sr("sdk_apply_flag_settings", async () => {
      await this.request({ subtype: "apply_flag_settings", settings: e });
    });
  }
  async getSettings() {
    return (await this.request({ subtype: "get_settings" })).response;
  }
  async rewindFiles(e, t) {
    return sr("sdk_rewind_files", async () => (await this.request({ subtype: "rewind_files", user_message_id: e, dry_run: t?.dryRun })).response);
  }
  async cancelAsyncMessage(e) {
    return (await this.request({ subtype: "cancel_async_message", message_uuid: e })).response.cancelled;
  }
  async seedReadState(e, t) {
    await this.request({ subtype: "seed_read_state", path: e, mtime: t });
  }
  async enableRemoteControl(e, t) {
    return (await this.request({ subtype: "remote_control", enabled: e, ...t !== void 0 && { name: t } })).response;
  }
  async submitFeedback(e, t) {
    return (await this.request({ subtype: "submit_feedback", description: e, surface: t?.surface })).response;
  }
  async generateSessionTitle(e, t) {
    return sr("sdk_session_title_generate", async () => (await this.request({ subtype: "generate_session_title", description: e, persist: t?.persist })).response.title);
  }
  async askSideQuestion(e) {
    return sr("sdk_side_question", async () => {
      let r = (await this.request({ subtype: "side_question", question: e })).response;
      return r.response === null ? null : { response: r.response, synthetic: r.synthetic ?? false };
    });
  }
  async launchUltrareview(e, t) {
    return (await this.request({ subtype: "ultrareview_launch", args: e, confirm: t?.confirm ?? false })).response;
  }
  async messageRated(e) {
    await this.request({ subtype: "message_rated", messageUuid: e.messageUuid, sentiment: e.sentiment, surface: e.surface, cleared: e.cleared ?? false });
  }
  processPendingPermissionRequests(e) {
    for (let t of e) if (t.request.subtype === "can_use_tool") this.handleControlRequest(t).catch(() => {
    });
  }
  processPendingUserDialogRequests(e) {
    for (let t of e) if (t.request.subtype === "request_user_dialog") this.handleControlRequest(t).catch(() => {
    });
  }
  request(e) {
    let t = Math.random().toString(36).substring(2, 15), r = { request_id: t, type: "control_request", request: e }, o = e.subtype === "initialize";
    return new Promise((n, i) => {
      this.pendingControlResponses.set(t, { handler: (s) => {
        if (this.pendingControlResponses.delete(t), s.subtype === "success") n(s);
        else i(Error(s.error));
        if (!o && (s.pending_permission_requests || s.pending_user_dialog_requests)) ee(`[Query] Ignoring prompt-redelivery fields on non-initialize response (subtype=${e.subtype})`);
        else {
          if (s.pending_permission_requests) this.processPendingPermissionRequests(s.pending_permission_requests);
          if (s.pending_user_dialog_requests) this.processPendingUserDialogRequests(s.pending_user_dialog_requests);
        }
      }, reject: i }), Promise.resolve(this.transport.write(pe(r) + `
`)).catch((s) => {
        this.pendingControlResponses.delete(t), i(s);
      });
    });
  }
  initializationResult() {
    return this.initialization;
  }
  async supportedCommands() {
    return (await this.initialization).commands;
  }
  async supportedModels() {
    return (await this.initialization).models;
  }
  async supportedAgents() {
    return (await this.initialization).agents;
  }
  async reconnectMcpServer(e) {
    await this.request({ subtype: "mcp_reconnect", serverName: e });
  }
  async toggleMcpServer(e, t) {
    return sr("sdk_mcp_toggle_server", async () => {
      await this.request({ subtype: "mcp_toggle", serverName: e, enabled: t });
    });
  }
  async enableChannel(e) {
    return sr("sdk_mcp_enable_channel", async () => {
      await this.request({ subtype: "channel_enable", serverName: e });
    });
  }
  async mcpAuthenticate(e, t) {
    return (await this.request({ subtype: "mcp_authenticate", serverName: e, redirectUri: t })).response;
  }
  async mcpClearAuth(e) {
    return (await this.request({ subtype: "mcp_clear_auth", serverName: e })).response;
  }
  async mcpSubmitOAuthCallbackUrl(e, t) {
    return (await this.request({ subtype: "mcp_oauth_callback_url", serverName: e, callbackUrl: t })).response;
  }
  async claudeAuthenticate(e) {
    return (await this.request({ subtype: "claude_authenticate", loginWithClaudeAi: e })).response;
  }
  async claudeOAuthCallback(e, t) {
    return (await this.request({ subtype: "claude_oauth_callback", authorizationCode: e, state: t })).response;
  }
  async claudeOAuthWaitForCompletion() {
    return (await this.request({ subtype: "claude_oauth_wait_for_completion" })).response;
  }
  async mcpServerStatus() {
    return (await this.request({ subtype: "mcp_status" })).response.mcpServers;
  }
  async getContextUsage() {
    return (await this.request({ subtype: "get_context_usage" })).response;
  }
  async usage_EXPERIMENTAL_MAY_CHANGE_DO_NOT_RELY_ON_THIS_API_YET() {
    return (await this.request({ subtype: "get_usage" })).response;
  }
  async readFile(e, t) {
    try {
      return (await this.request({ subtype: "read_file", path: e, max_bytes: t?.maxBytes, encoding: t?.encoding })).response;
    } catch {
      return null;
    }
  }
  async reloadPlugins() {
    return sr("sdk_reload_plugins", async () => (await this.request({ subtype: "reload_plugins" })).response);
  }
  async reloadSkills() {
    return sr("sdk_reload_skills", async () => (await this.request({ subtype: "reload_skills" })).response);
  }
  async setMcpServers(e) {
    return sr("sdk_mcp_set_servers", async () => {
      let t = {}, r = {};
      for (let [a, c] of Object.entries(e)) if (c.type === "sdk" && "instance" in c) t[a] = c.instance;
      else r[a] = c;
      let o = new Set(this.sdkMcpServerInstances.keys()), n = new Set(Object.keys(t));
      for (let a of o) if (!n.has(a)) await this.disconnectSdkMcpServer(a);
      for (let [a, c] of Object.entries(t)) if (!o.has(a)) this.connectSdkMcpServer(a, c);
      let i = {};
      for (let a of Object.keys(t)) i[a] = { type: "sdk", name: a };
      return (await this.request({ subtype: "mcp_set_servers", servers: { ...r, ...i } })).response;
    });
  }
  async accountInfo() {
    return (await this.initialization).account;
  }
  async streamInput(e) {
    ee("[Query.streamInput] Starting to process input stream");
    try {
      let t = 0;
      for await (let r of e) {
        if (t++, ee(`[Query.streamInput] Processing message ${t}: ${r.type}`), this.abortController?.signal.aborted) break;
        await Promise.resolve(this.transport.write(pe(r) + `
`));
      }
      if (ee(`[Query.streamInput] Finished processing ${t} messages from input stream`), t > 0 && this.hasBidirectionalNeeds()) ee("[Query.streamInput] Has bidirectional needs, waiting for first result"), await this.waitForFirstResult();
      ee("[Query] Calling transport.endInput() to close stdin to CLI process"), this.transport.endInput();
    } catch (t) {
      if (!(t instanceof ot)) throw t;
    }
  }
  waitForFirstResult() {
    if (this.firstResultReceived) return ee("[Query.waitForFirstResult] Result already received, returning immediately"), Promise.resolve();
    return new Promise((e) => {
      if (this.abortController?.signal.aborted) {
        e();
        return;
      }
      this.abortController?.signal.addEventListener("abort", () => e(), { once: true }), this.firstResultReceivedResolve = e;
    });
  }
  handleHookCallbacks(e, t, r, o) {
    let n = this.hookCallbacks.get(e);
    if (!n) throw Error(`No hook callback found for ID: ${e}`);
    return n(t, r, { signal: o });
  }
  connectSdkMcpServer(e, t) {
    let r = new Uh((o) => this.sendMcpServerMessageToCli(e, o));
    this.sdkMcpTransports.set(e, r), this.sdkMcpServerInstances.set(e, t), t.connect(r).catch((o) => {
      if (this.sdkMcpTransports.get(e) === r) this.sdkMcpTransports.delete(e);
      if (this.sdkMcpServerInstances.get(e) === t) this.sdkMcpServerInstances.delete(e);
      ee(`[Query.connectSdkMcpServer] Failed to connect MCP server '${e}': ${o}`, { level: "error" });
    });
  }
  async disconnectSdkMcpServer(e) {
    let t = this.sdkMcpTransports.get(e);
    if (t) await t.close(), this.sdkMcpTransports.delete(e);
    this.sdkMcpServerInstances.delete(e);
  }
  sendMcpServerMessageToCli(e, t) {
    if ("id" in t && t.id !== null && t.id !== void 0) {
      let o = `${e}:${t.id}`, n = this.pendingMcpResponses.get(o);
      if (n) {
        n.resolve(t), this.pendingMcpResponses.delete(o);
        return;
      }
    }
    let r = { type: "control_request", request_id: Ha(), request: { subtype: "mcp_message", server_name: e, message: t } };
    Promise.resolve(this.transport.write(pe(r) + `
`)).catch((o) => {
      ee(`[Query.sendMcpServerMessageToCli] Transport write failed: ${o}`, { level: "error" });
    });
  }
  handleMcpControlRequest(e, t, r) {
    let o = "id" in t.message ? t.message.id : null, n = `${e}:${o}`;
    return new Promise((i, s) => {
      let a = () => {
        this.pendingMcpResponses.delete(n);
      }, c = (d) => {
        a(), i(d);
      }, u = (d) => {
        a(), s(d);
      };
      if (this.pendingMcpResponses.set(n, { resolve: c, reject: u }), r.onmessage) r.onmessage(t.message);
      else {
        a(), s(Error("No message handler registered"));
        return;
      }
    });
  }
};
var vd = 500;
var Sd = 1048576;
var PB = [200, 800];
var Lh = class {
  send;
  sendTimeoutMs;
  onError;
  maxPendingEntries;
  maxPendingBytes;
  backoffMs;
  pending = [];
  pendingEntries = 0;
  pendingBytes = 0;
  flushPromise = null;
  constructor(e, t = 6e4, r, o = vd, n = Sd, i = PB) {
    this.send = e;
    this.sendTimeoutMs = t;
    this.onError = r;
    this.maxPendingEntries = o;
    this.maxPendingBytes = n;
    this.backoffMs = i;
  }
  enqueue(e, t) {
    let r = pe(t).length;
    if (this.pending.push({ filePath: e, entries: t, bytes: r }), this.pendingEntries += t.length, this.pendingBytes += r, this.pendingEntries > this.maxPendingEntries || this.pendingBytes > this.maxPendingBytes) this.flushPromise = this.drain(), this.flushPromise.catch(() => {
    });
  }
  async flush() {
    let e = this.drain();
    if (this.flushPromise = e, await e, this.flushPromise === e) this.flushPromise = null;
  }
  async drain() {
    let e = this.flushPromise, t = this.pending.splice(0);
    if (this.pendingEntries = 0, this.pendingBytes = 0, e) await e;
    if (t.length === 0) return;
    await this.doFlush(t);
  }
  async doFlush(e) {
    let t = /* @__PURE__ */ new Map();
    for (let o of e) {
      let n = t.get(o.filePath);
      if (n) n.push(...o.entries);
      else t.set(o.filePath, o.entries.slice());
    }
    let r = this.backoffMs.length + 1;
    for (let [o, n] of t) {
      let i = `SessionStore.append() timed out after ${this.sendTimeoutMs}ms for ${o}`, s, a = 1;
      for (; a <= r; a++) try {
        await Ar(this.send(o, n), this.sendTimeoutMs, i), s = void 0;
        break;
      } catch (c) {
        if (s = kr(c), s.message === i) break;
        let u = this.backoffMs[a - 1];
        if (u === void 0) break;
        await yu(u);
      }
      if (s) {
        ee(`[TranscriptMirrorBatcher] flush failed for ${o} after ${a} attempt(s): ${s}`, { level: "error" });
        try {
          this.onError?.(o, s);
        } catch (c) {
          ee(`[TranscriptMirrorBatcher] onError callback threw: ${c}`, { level: "error" });
        }
      }
    }
  }
};
var _g = Tg(UR(), 1);
var r6 = t6(e6);
function My(e) {
  let t = 0;
  for (let r = 0; r < e.length; r++) t = (t << 5) - t + e.charCodeAt(r) | 0;
  return t;
}
var s6 = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
function Se(e) {
  if (typeof e !== "string") return null;
  return s6.test(e) ? e : null;
}
async function ec(e, t) {
  let r = n6(e, { mode: 384 });
  try {
    for (let o of t) if (!r.write(JSON.stringify(o) + `
`)) await FR(r, "drain");
    r.end(), await FR(r, "finish");
  } catch (o) {
    throw r.destroy(), o;
  }
}
var Di = 200;
function a6(e) {
  return Math.abs(My(e)).toString(36);
}
function So(e) {
  let t = e.replace(/[^a-zA-Z0-9]/g, "-");
  if (t.length <= Di) return t;
  return `${t.slice(0, Di)}-${a6(e)}`;
}
var $d = Buffer.from('{"type":"attribution-snapshot"');
var p6 = Buffer.from('{"type":"system"');
var Qa = 10;
var f6 = Buffer.from([Qa]);
function Zy(e, t) {
  let r = 0;
  for (let o of e) r += +!!t(o);
  return r;
}
function Dd(e) {
  return [...new Set(e)];
}
function W6() {
  return "prod";
}
var K6 = "user:inference";
var y0 = "user:profile";
var G6 = "org:create_api_key";
var J6 = [G6, y0];
var X6 = [y0, K6, "user:sessions:claude_code", "user:mcp_servers", "user:file_upload", ...[]];
var lbe = Dd([...J6, ...X6]);
var h0 = { BASE_API_URL: "https://api.anthropic.com", CONSOLE_AUTHORIZE_URL: "https://platform.claude.com/oauth/authorize", CLAUDE_AI_AUTHORIZE_URL: "https://claude.com/cai/oauth/authorize", CLAUDE_AI_ORIGIN: "https://claude.ai", TOKEN_URL: "https://platform.claude.com/v1/oauth/token", API_KEY_URL: "https://api.anthropic.com/api/oauth/claude_cli/create_api_key", ROLES_URL: "https://api.anthropic.com/api/oauth/claude_cli/roles", CONSOLE_SUCCESS_URL: "https://platform.claude.com/buy_credits?returnUrl=/oauth/code/success%3Fapp%3Dclaude-code", CLAUDEAI_SUCCESS_URL: "https://platform.claude.com/oauth/code/success?app=claude-code", MANUAL_REDIRECT_URL: "https://platform.claude.com/oauth/code/callback", CLIENT_ID: "9d1c250a-e61b-44d9-88ed-5944d1962f5e", DESIGN_CLIENT_ID: "59637612-477b-4836-a601-b0589eda7704", OAUTH_FILE_SUFFIX: "", MCP_PROXY_URL: "https://mcp-proxy.anthropic.com", MCP_PROXY_PATH: "/v1/mcp/{server_id}" };
var Y6 = void 0;
function Q6() {
  let e = process.env.CLAUDE_LOCAL_OAUTH_API_BASE?.replace(/\/$/, "") ?? "http://localhost:8000", t = process.env.CLAUDE_LOCAL_OAUTH_APPS_BASE?.replace(/\/$/, "") ?? "http://localhost:4000", r = process.env.CLAUDE_LOCAL_OAUTH_CONSOLE_BASE?.replace(/\/$/, "") ?? "http://localhost:3000";
  return { BASE_API_URL: e, CONSOLE_AUTHORIZE_URL: `${r}/oauth/authorize`, CLAUDE_AI_AUTHORIZE_URL: `${t}/oauth/authorize`, CLAUDE_AI_ORIGIN: t, TOKEN_URL: `${e}/v1/oauth/token`, API_KEY_URL: `${e}/api/oauth/claude_cli/create_api_key`, ROLES_URL: `${e}/api/oauth/claude_cli/roles`, CONSOLE_SUCCESS_URL: `${r}/buy_credits?returnUrl=/oauth/code/success%3Fapp%3Dclaude-code`, CLAUDEAI_SUCCESS_URL: `${r}/oauth/code/success?app=claude-code`, MANUAL_REDIRECT_URL: `${r}/oauth/code/callback`, CLIENT_ID: "22422756-60c9-4084-8eb7-27705fd5cf9a", DESIGN_CLIENT_ID: "00000000-0000-4000-8000-000000000000", OAUTH_FILE_SUFFIX: "-local-oauth", MCP_PROXY_URL: "http://localhost:8205", MCP_PROXY_PATH: "/v1/toolbox/shttp/mcp/{server_id}" };
}
var eZ = ["https://beacon.claude-ai.staging.ant.dev", "https://claude.fedstart.com", "https://claude-staging.fedstart.com"];
function b0() {
  let e = (() => {
    switch (W6()) {
      case "local":
        return Q6();
      case "staging":
        return Y6 ?? h0;
      case "prod":
        return h0;
    }
  })(), t = process.env.CLAUDE_CODE_CUSTOM_OAUTH_URL;
  if (t) {
    let o = t.replace(/\/$/, "");
    if (!eZ.includes(o)) throw Error("CLAUDE_CODE_CUSTOM_OAUTH_URL is not an approved endpoint.");
    e = { ...e, BASE_API_URL: o, CONSOLE_AUTHORIZE_URL: `${o}/oauth/authorize`, CLAUDE_AI_AUTHORIZE_URL: `${o}/oauth/authorize`, CLAUDE_AI_ORIGIN: o, TOKEN_URL: `${o}/v1/oauth/token`, API_KEY_URL: `${o}/api/oauth/claude_cli/create_api_key`, ROLES_URL: `${o}/api/oauth/claude_cli/roles`, CONSOLE_SUCCESS_URL: `${o}/oauth/code/success?app=claude-code`, CLAUDEAI_SUCCESS_URL: `${o}/oauth/code/success?app=claude-code`, MANUAL_REDIRECT_URL: `${o}/oauth/code/callback`, OAUTH_FILE_SUFFIX: "-custom-oauth" };
  }
  let r = process.env.CLAUDE_CODE_OAUTH_CLIENT_ID;
  if (r) e = { ...e, CLIENT_ID: r };
  return e;
}
var _0 = "-credentials";
function v0(e = "") {
  let t = process.env.CLAUDE_SECURESTORAGE_CONFIG_DIR, r = t !== void 0 ? !t : !process.env.CLAUDE_CONFIG_DIR, o = t !== void 0 ? t.normalize("NFC") : qt(), n = r ? "" : `-${tZ("sha256").update(o).digest("hex").substring(0, 8)}`;
  return `Claude Code${b0().OAUTH_FILE_SUFFIX}${e}${n}`;
}
var nZ = /^[a-zA-Z0-9._-]+$/;
function S0() {
  if (process.platform === "win32") return "claude-code-user";
  let e;
  try {
    e = process.env.USER || rZ().username;
  } catch {
    e = "claude-code-user";
  }
  if (!nZ.test(e)) return "claude-code-user";
  return e;
}
var ae;
(function(e) {
  e.assertEqual = (n) => {
  };
  function t(n) {
  }
  e.assertIs = t;
  function r(n) {
    throw Error();
  }
  e.assertNever = r, e.arrayToEnum = (n) => {
    let i = {};
    for (let s of n) i[s] = s;
    return i;
  }, e.getValidEnumValues = (n) => {
    let i = e.objectKeys(n).filter((a) => typeof n[n[a]] !== "number"), s = {};
    for (let a of i) s[a] = n[a];
    return e.objectValues(s);
  }, e.objectValues = (n) => e.objectKeys(n).map(function(i) {
    return n[i];
  }), e.objectKeys = typeof Object.keys === "function" ? (n) => Object.keys(n) : (n) => {
    let i = [];
    for (let s in n) if (Object.prototype.hasOwnProperty.call(n, s)) i.push(s);
    return i;
  }, e.find = (n, i) => {
    for (let s of n) if (i(s)) return s;
    return;
  }, e.isInteger = typeof Number.isInteger === "function" ? (n) => Number.isInteger(n) : (n) => typeof n === "number" && Number.isFinite(n) && Math.floor(n) === n;
  function o(n, i = " | ") {
    return n.map((s) => typeof s === "string" ? `'${s}'` : s).join(i);
  }
  e.joinValues = o, e.jsonStringifyReplacer = (n, i) => {
    if (typeof i === "bigint") return i.toString();
    return i;
  };
})(ae || (ae = {}));
var x0;
(function(e) {
  e.mergeShapes = (t, r) => ({ ...t, ...r });
})(x0 || (x0 = {}));
var C = ae.arrayToEnum(["string", "nan", "number", "integer", "float", "boolean", "date", "bigint", "symbol", "function", "undefined", "null", "array", "object", "unknown", "promise", "void", "never", "map", "set"]);
var Hr = (e) => {
  switch (typeof e) {
    case "undefined":
      return C.undefined;
    case "string":
      return C.string;
    case "number":
      return Number.isNaN(e) ? C.nan : C.number;
    case "boolean":
      return C.boolean;
    case "function":
      return C.function;
    case "bigint":
      return C.bigint;
    case "symbol":
      return C.symbol;
    case "object":
      if (Array.isArray(e)) return C.array;
      if (e === null) return C.null;
      if (e.then && typeof e.then === "function" && e.catch && typeof e.catch === "function") return C.promise;
      if (typeof Map < "u" && e instanceof Map) return C.map;
      if (typeof Set < "u" && e instanceof Set) return C.set;
      if (typeof Date < "u" && e instanceof Date) return C.date;
      return C.object;
    default:
      return C.unknown;
  }
};
var I = ae.arrayToEnum(["invalid_type", "invalid_literal", "custom", "invalid_union", "invalid_union_discriminator", "invalid_enum_value", "unrecognized_keys", "invalid_arguments", "invalid_return_type", "invalid_date", "invalid_string", "too_small", "too_big", "invalid_intersection_types", "not_multiple_of", "not_finite"]);
var Nt = class _Nt extends Error {
  get errors() {
    return this.issues;
  }
  constructor(e) {
    super();
    this.issues = [], this.addIssue = (r) => {
      this.issues = [...this.issues, r];
    }, this.addIssues = (r = []) => {
      this.issues = [...this.issues, ...r];
    };
    let t = new.target.prototype;
    if (Object.setPrototypeOf) Object.setPrototypeOf(this, t);
    else this.__proto__ = t;
    this.name = "ZodError", this.issues = e;
  }
  format(e) {
    let t = e || function(n) {
      return n.message;
    }, r = { _errors: [] }, o = (n) => {
      for (let i of n.issues) if (i.code === "invalid_union") i.unionErrors.map(o);
      else if (i.code === "invalid_return_type") o(i.returnTypeError);
      else if (i.code === "invalid_arguments") o(i.argumentsError);
      else if (i.path.length === 0) r._errors.push(t(i));
      else {
        let s = r, a = 0;
        while (a < i.path.length) {
          let c = i.path[a];
          if (a !== i.path.length - 1) s[c] = s[c] || { _errors: [] };
          else s[c] = s[c] || { _errors: [] }, s[c]._errors.push(t(i));
          s = s[c], a++;
        }
      }
    };
    return o(this), r;
  }
  static assert(e) {
    if (!(e instanceof _Nt)) throw Error(`Not a ZodError: ${e}`);
  }
  toString() {
    return this.message;
  }
  get message() {
    return JSON.stringify(this.issues, ae.jsonStringifyReplacer, 2);
  }
  get isEmpty() {
    return this.issues.length === 0;
  }
  flatten(e = (t) => t.message) {
    let t = {}, r = [];
    for (let o of this.issues) if (o.path.length > 0) {
      let n = o.path[0];
      t[n] = t[n] || [], t[n].push(e(o));
    } else r.push(e(o));
    return { formErrors: r, fieldErrors: t };
  }
  get formErrors() {
    return this.flatten();
  }
};
Nt.create = (e) => new Nt(e);
var oZ = (e, t) => {
  let r;
  switch (e.code) {
    case I.invalid_type:
      if (e.received === C.undefined) r = "Required";
      else r = `Expected ${e.expected}, received ${e.received}`;
      break;
    case I.invalid_literal:
      r = `Invalid literal value, expected ${JSON.stringify(e.expected, ae.jsonStringifyReplacer)}`;
      break;
    case I.unrecognized_keys:
      r = `Unrecognized key(s) in object: ${ae.joinValues(e.keys, ", ")}`;
      break;
    case I.invalid_union:
      r = "Invalid input";
      break;
    case I.invalid_union_discriminator:
      r = `Invalid discriminator value. Expected ${ae.joinValues(e.options)}`;
      break;
    case I.invalid_enum_value:
      r = `Invalid enum value. Expected ${ae.joinValues(e.options)}, received '${e.received}'`;
      break;
    case I.invalid_arguments:
      r = "Invalid function arguments";
      break;
    case I.invalid_return_type:
      r = "Invalid function return type";
      break;
    case I.invalid_date:
      r = "Invalid date";
      break;
    case I.invalid_string:
      if (typeof e.validation === "object") if ("includes" in e.validation) {
        if (r = `Invalid input: must include "${e.validation.includes}"`, typeof e.validation.position === "number") r = `${r} at one or more positions greater than or equal to ${e.validation.position}`;
      } else if ("startsWith" in e.validation) r = `Invalid input: must start with "${e.validation.startsWith}"`;
      else if ("endsWith" in e.validation) r = `Invalid input: must end with "${e.validation.endsWith}"`;
      else ae.assertNever(e.validation);
      else if (e.validation !== "regex") r = `Invalid ${e.validation}`;
      else r = "Invalid";
      break;
    case I.too_small:
      if (e.type === "array") r = `Array must contain ${e.exact ? "exactly" : e.inclusive ? "at least" : "more than"} ${e.minimum} element(s)`;
      else if (e.type === "string") r = `String must contain ${e.exact ? "exactly" : e.inclusive ? "at least" : "over"} ${e.minimum} character(s)`;
      else if (e.type === "number") r = `Number must be ${e.exact ? "exactly equal to " : e.inclusive ? "greater than or equal to " : "greater than "}${e.minimum}`;
      else if (e.type === "bigint") r = `Number must be ${e.exact ? "exactly equal to " : e.inclusive ? "greater than or equal to " : "greater than "}${e.minimum}`;
      else if (e.type === "date") r = `Date must be ${e.exact ? "exactly equal to " : e.inclusive ? "greater than or equal to " : "greater than "}${new Date(Number(e.minimum))}`;
      else r = "Invalid input";
      break;
    case I.too_big:
      if (e.type === "array") r = `Array must contain ${e.exact ? "exactly" : e.inclusive ? "at most" : "less than"} ${e.maximum} element(s)`;
      else if (e.type === "string") r = `String must contain ${e.exact ? "exactly" : e.inclusive ? "at most" : "under"} ${e.maximum} character(s)`;
      else if (e.type === "number") r = `Number must be ${e.exact ? "exactly" : e.inclusive ? "less than or equal to" : "less than"} ${e.maximum}`;
      else if (e.type === "bigint") r = `BigInt must be ${e.exact ? "exactly" : e.inclusive ? "less than or equal to" : "less than"} ${e.maximum}`;
      else if (e.type === "date") r = `Date must be ${e.exact ? "exactly" : e.inclusive ? "smaller than or equal to" : "smaller than"} ${new Date(Number(e.maximum))}`;
      else r = "Invalid input";
      break;
    case I.custom:
      r = "Invalid input";
      break;
    case I.invalid_intersection_types:
      r = "Intersection results could not be merged";
      break;
    case I.not_multiple_of:
      r = `Number must be a multiple of ${e.multipleOf}`;
      break;
    case I.not_finite:
      r = "Number must be finite";
      break;
    default:
      r = t.defaultError, ae.assertNever(e);
  }
  return { message: r };
};
var En = oZ;
var iZ = En;
function rc() {
  return iZ;
}
var Nd = (e) => {
  let { data: t, path: r, errorMaps: o, issueData: n } = e, i = [...r, ...n.path || []], s = { ...n, path: i };
  if (n.message !== void 0) return { ...n, path: i, message: n.message };
  let a = "", c = o.filter((u) => !!u).slice().reverse();
  for (let u of c) a = u(s, { data: t, defaultError: a }).message;
  return { ...n, path: i, message: a };
};
function j(e, t) {
  let r = rc(), o = Nd({ issueData: t, data: e.data, path: e.path, errorMaps: [e.common.contextualErrorMap, e.schemaErrorMap, r, r === En ? void 0 : En].filter((n) => !!n) });
  e.common.issues.push(o);
}
var st = class _st {
  constructor() {
    this.value = "valid";
  }
  dirty() {
    if (this.value === "valid") this.value = "dirty";
  }
  abort() {
    if (this.value !== "aborted") this.value = "aborted";
  }
  static mergeArray(e, t) {
    let r = [];
    for (let o of t) {
      if (o.status === "aborted") return W;
      if (o.status === "dirty") e.dirty();
      r.push(o.value);
    }
    return { status: e.value, value: r };
  }
  static async mergeObjectAsync(e, t) {
    let r = [];
    for (let o of t) {
      let n = await o.key, i = await o.value;
      r.push({ key: n, value: i });
    }
    return _st.mergeObjectSync(e, r);
  }
  static mergeObjectSync(e, t) {
    let r = {};
    for (let o of t) {
      let { key: n, value: i } = o;
      if (n.status === "aborted") return W;
      if (i.status === "aborted") return W;
      if (n.status === "dirty") e.dirty();
      if (i.status === "dirty") e.dirty();
      if (n.value !== "__proto__" && (typeof i.value < "u" || o.alwaysSet)) r[n.value] = i.value;
    }
    return { status: e.value, value: r };
  }
};
var W = Object.freeze({ status: "aborted" });
var Ui = (e) => ({ status: "dirty", value: e });
var pt = (e) => ({ status: "valid", value: e });
var Vy = (e) => e.status === "aborted";
var Wy = (e) => e.status === "dirty";
var xo = (e) => e.status === "valid";
var nc = (e) => typeof Promise < "u" && e instanceof Promise;
var F;
(function(e) {
  e.errToObj = (t) => typeof t === "string" ? { message: t } : t || {}, e.toString = (t) => typeof t === "string" ? t : t?.message;
})(F || (F = {}));
var lr = class {
  constructor(e, t, r, o) {
    this._cachedPath = [], this.parent = e, this.data = t, this._path = r, this._key = o;
  }
  get path() {
    if (!this._cachedPath.length) if (Array.isArray(this._key)) this._cachedPath.push(...this._path, ...this._key);
    else this._cachedPath.push(...this._path, this._key);
    return this._cachedPath;
  }
};
var w0 = (e, t) => {
  if (xo(t)) return { success: true, data: t.value };
  else {
    if (!e.common.issues.length) throw Error("Validation failed but no issues detected.");
    return { success: false, get error() {
      if (this._error) return this._error;
      let r = new Nt(e.common.issues);
      return this._error = r, this._error;
    } };
  }
};
function Q(e) {
  if (!e) return {};
  let { errorMap: t, invalid_type_error: r, required_error: o, description: n } = e;
  if (t && (r || o)) throw Error(`Can't use "invalid_type_error" or "required_error" in conjunction with custom error map.`);
  if (t) return { errorMap: t, description: n };
  return { errorMap: (s, a) => {
    let { message: c } = e;
    if (s.code === "invalid_enum_value") return { message: c ?? a.defaultError };
    if (typeof a.data > "u") return { message: c ?? o ?? a.defaultError };
    if (s.code !== "invalid_type") return { message: a.defaultError };
    return { message: c ?? r ?? a.defaultError };
  }, description: n };
}
var oe = class {
  get description() {
    return this._def.description;
  }
  _getType(e) {
    return Hr(e.data);
  }
  _getOrReturnCtx(e, t) {
    return t || { common: e.parent.common, data: e.data, parsedType: Hr(e.data), schemaErrorMap: this._def.errorMap, path: e.path, parent: e.parent };
  }
  _processInputParams(e) {
    return { status: new st(), ctx: { common: e.parent.common, data: e.data, parsedType: Hr(e.data), schemaErrorMap: this._def.errorMap, path: e.path, parent: e.parent } };
  }
  _parseSync(e) {
    let t = this._parse(e);
    if (nc(t)) throw Error("Synchronous parse encountered promise.");
    return t;
  }
  _parseAsync(e) {
    let t = this._parse(e);
    return Promise.resolve(t);
  }
  parse(e, t) {
    let r = this.safeParse(e, t);
    if (r.success) return r.data;
    throw r.error;
  }
  safeParse(e, t) {
    let r = { common: { issues: [], async: t?.async ?? false, contextualErrorMap: t?.errorMap }, path: t?.path || [], schemaErrorMap: this._def.errorMap, parent: null, data: e, parsedType: Hr(e) }, o = this._parseSync({ data: e, path: r.path, parent: r });
    return w0(r, o);
  }
  "~validate"(e) {
    let t = { common: { issues: [], async: !!this["~standard"].async }, path: [], schemaErrorMap: this._def.errorMap, parent: null, data: e, parsedType: Hr(e) };
    if (!this["~standard"].async) try {
      let r = this._parseSync({ data: e, path: [], parent: t });
      return xo(r) ? { value: r.value } : { issues: t.common.issues };
    } catch (r) {
      if (r?.message?.toLowerCase()?.includes("encountered")) this["~standard"].async = true;
      t.common = { issues: [], async: true };
    }
    return this._parseAsync({ data: e, path: [], parent: t }).then((r) => xo(r) ? { value: r.value } : { issues: t.common.issues });
  }
  async parseAsync(e, t) {
    let r = await this.safeParseAsync(e, t);
    if (r.success) return r.data;
    throw r.error;
  }
  async safeParseAsync(e, t) {
    let r = { common: { issues: [], contextualErrorMap: t?.errorMap, async: true }, path: t?.path || [], schemaErrorMap: this._def.errorMap, parent: null, data: e, parsedType: Hr(e) }, o = this._parse({ data: e, path: r.path, parent: r }), n = await (nc(o) ? o : Promise.resolve(o));
    return w0(r, n);
  }
  refine(e, t) {
    let r = (o) => {
      if (typeof t === "string" || typeof t > "u") return { message: t };
      else if (typeof t === "function") return t(o);
      else return t;
    };
    return this._refinement((o, n) => {
      let i = e(o), s = () => n.addIssue({ code: I.custom, ...r(o) });
      if (typeof Promise < "u" && i instanceof Promise) return i.then((a) => {
        if (!a) return s(), false;
        else return true;
      });
      if (!i) return s(), false;
      else return true;
    });
  }
  refinement(e, t) {
    return this._refinement((r, o) => {
      if (!e(r)) return o.addIssue(typeof t === "function" ? t(r, o) : t), false;
      else return true;
    });
  }
  _refinement(e) {
    return new Tr({ schema: this, typeName: R.ZodEffects, effect: { type: "refinement", refinement: e } });
  }
  superRefine(e) {
    return this._refinement(e);
  }
  constructor(e) {
    this.spa = this.safeParseAsync, this._def = e, this.parse = this.parse.bind(this), this.safeParse = this.safeParse.bind(this), this.parseAsync = this.parseAsync.bind(this), this.safeParseAsync = this.safeParseAsync.bind(this), this.spa = this.spa.bind(this), this.refine = this.refine.bind(this), this.refinement = this.refinement.bind(this), this.superRefine = this.superRefine.bind(this), this.optional = this.optional.bind(this), this.nullable = this.nullable.bind(this), this.nullish = this.nullish.bind(this), this.array = this.array.bind(this), this.promise = this.promise.bind(this), this.or = this.or.bind(this), this.and = this.and.bind(this), this.transform = this.transform.bind(this), this.brand = this.brand.bind(this), this.default = this.default.bind(this), this.catch = this.catch.bind(this), this.describe = this.describe.bind(this), this.pipe = this.pipe.bind(this), this.readonly = this.readonly.bind(this), this.isNullable = this.isNullable.bind(this), this.isOptional = this.isOptional.bind(this), this["~standard"] = { version: 1, vendor: "zod", validate: (t) => this["~validate"](t) };
  }
  optional() {
    return Gt.create(this, this._def);
  }
  nullable() {
    return Tn.create(this, this._def);
  }
  nullish() {
    return this.nullable().optional();
  }
  array() {
    return Er.create(this);
  }
  promise() {
    return Hi.create(this, this._def);
  }
  or(e) {
    return cc.create([this, e], this._def);
  }
  and(e) {
    return lc.create(this, e, this._def);
  }
  transform(e) {
    return new Tr({ ...Q(this._def), schema: this, typeName: R.ZodEffects, effect: { type: "transform", transform: e } });
  }
  default(e) {
    let t = typeof e === "function" ? e : () => e;
    return new fc({ ...Q(this._def), innerType: this, defaultValue: t, typeName: R.ZodDefault });
  }
  brand() {
    return new Xy({ typeName: R.ZodBranded, type: this, ...Q(this._def) });
  }
  catch(e) {
    let t = typeof e === "function" ? e : () => e;
    return new mc({ ...Q(this._def), innerType: this, catchValue: t, typeName: R.ZodCatch });
  }
  describe(e) {
    return new this.constructor({ ...this._def, description: e });
  }
  pipe(e) {
    return qd.create(this, e);
  }
  readonly() {
    return gc.create(this);
  }
  isOptional() {
    return this.safeParse(void 0).success;
  }
  isNullable() {
    return this.safeParse(null).success;
  }
};
var sZ = /^c[^\s-]{8,}$/i;
var aZ = /^[0-9a-z]+$/;
var cZ = /^[0-9A-HJKMNP-TV-Z]{26}$/i;
var lZ = /^[0-9a-fA-F]{8}\b-[0-9a-fA-F]{4}\b-[0-9a-fA-F]{4}\b-[0-9a-fA-F]{4}\b-[0-9a-fA-F]{12}$/i;
var uZ = /^[a-z0-9_-]{21}$/i;
var dZ = /^[A-Za-z0-9-_]+\.[A-Za-z0-9-_]+\.[A-Za-z0-9-_]*$/;
var pZ = /^[-+]?P(?!$)(?:(?:[-+]?\d+Y)|(?:[-+]?\d+[.,]\d+Y$))?(?:(?:[-+]?\d+M)|(?:[-+]?\d+[.,]\d+M$))?(?:(?:[-+]?\d+W)|(?:[-+]?\d+[.,]\d+W$))?(?:(?:[-+]?\d+D)|(?:[-+]?\d+[.,]\d+D$))?(?:T(?=[\d+-])(?:(?:[-+]?\d+H)|(?:[-+]?\d+[.,]\d+H$))?(?:(?:[-+]?\d+M)|(?:[-+]?\d+[.,]\d+M$))?(?:[-+]?\d+(?:[.,]\d+)?S)?)??$/;
var fZ = /^(?!\.)(?!.*\.\.)([A-Z0-9_'+\-\.]*)[A-Z0-9_+-]@([A-Z0-9][A-Z0-9\-]*\.)+[A-Z]{2,}$/i;
var mZ = "^(\\p{Extended_Pictographic}|\\p{Emoji_Component})+$";
var Ky;
var gZ = /^(?:(?:25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9][0-9]|[0-9])\.){3}(?:25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9][0-9]|[0-9])$/;
var hZ = /^(?:(?:25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9][0-9]|[0-9])\.){3}(?:25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9][0-9]|[0-9])\/(3[0-2]|[12]?[0-9])$/;
var yZ = /^(([0-9a-fA-F]{1,4}:){7,7}[0-9a-fA-F]{1,4}|([0-9a-fA-F]{1,4}:){1,7}:|([0-9a-fA-F]{1,4}:){1,6}:[0-9a-fA-F]{1,4}|([0-9a-fA-F]{1,4}:){1,5}(:[0-9a-fA-F]{1,4}){1,2}|([0-9a-fA-F]{1,4}:){1,4}(:[0-9a-fA-F]{1,4}){1,3}|([0-9a-fA-F]{1,4}:){1,3}(:[0-9a-fA-F]{1,4}){1,4}|([0-9a-fA-F]{1,4}:){1,2}(:[0-9a-fA-F]{1,4}){1,5}|[0-9a-fA-F]{1,4}:((:[0-9a-fA-F]{1,4}){1,6})|:((:[0-9a-fA-F]{1,4}){1,7}|:)|fe80:(:[0-9a-fA-F]{0,4}){0,4}%[0-9a-zA-Z]{1,}|::(ffff(:0{1,4}){0,1}:){0,1}((25[0-5]|(2[0-4]|1{0,1}[0-9]){0,1}[0-9])\.){3,3}(25[0-5]|(2[0-4]|1{0,1}[0-9]){0,1}[0-9])|([0-9a-fA-F]{1,4}:){1,4}:((25[0-5]|(2[0-4]|1{0,1}[0-9]){0,1}[0-9])\.){3,3}(25[0-5]|(2[0-4]|1{0,1}[0-9]){0,1}[0-9]))$/;
var bZ = /^(([0-9a-fA-F]{1,4}:){7,7}[0-9a-fA-F]{1,4}|([0-9a-fA-F]{1,4}:){1,7}:|([0-9a-fA-F]{1,4}:){1,6}:[0-9a-fA-F]{1,4}|([0-9a-fA-F]{1,4}:){1,5}(:[0-9a-fA-F]{1,4}){1,2}|([0-9a-fA-F]{1,4}:){1,4}(:[0-9a-fA-F]{1,4}){1,3}|([0-9a-fA-F]{1,4}:){1,3}(:[0-9a-fA-F]{1,4}){1,4}|([0-9a-fA-F]{1,4}:){1,2}(:[0-9a-fA-F]{1,4}){1,5}|[0-9a-fA-F]{1,4}:((:[0-9a-fA-F]{1,4}){1,6})|:((:[0-9a-fA-F]{1,4}){1,7}|:)|fe80:(:[0-9a-fA-F]{0,4}){0,4}%[0-9a-zA-Z]{1,}|::(ffff(:0{1,4}){0,1}:){0,1}((25[0-5]|(2[0-4]|1{0,1}[0-9]){0,1}[0-9])\.){3,3}(25[0-5]|(2[0-4]|1{0,1}[0-9]){0,1}[0-9])|([0-9a-fA-F]{1,4}:){1,4}:((25[0-5]|(2[0-4]|1{0,1}[0-9]){0,1}[0-9])\.){3,3}(25[0-5]|(2[0-4]|1{0,1}[0-9]){0,1}[0-9]))\/(12[0-8]|1[01][0-9]|[1-9]?[0-9])$/;
var _Z = /^([0-9a-zA-Z+/]{4})*(([0-9a-zA-Z+/]{2}==)|([0-9a-zA-Z+/]{3}=))?$/;
var vZ = /^([0-9a-zA-Z-_]{4})*(([0-9a-zA-Z-_]{2}(==)?)|([0-9a-zA-Z-_]{3}(=)?))?$/;
var k0 = "((\\d\\d[2468][048]|\\d\\d[13579][26]|\\d\\d0[48]|[02468][048]00|[13579][26]00)-02-29|\\d{4}-((0[13578]|1[02])-(0[1-9]|[12]\\d|3[01])|(0[469]|11)-(0[1-9]|[12]\\d|30)|(02)-(0[1-9]|1\\d|2[0-8])))";
var SZ = new RegExp(`^${k0}$`);
function E0(e) {
  let t = "[0-5]\\d";
  if (e.precision) t = `${t}\\.\\d{${e.precision}}`;
  else if (e.precision == null) t = `${t}(\\.\\d+)?`;
  let r = e.precision ? "+" : "?";
  return `([01]\\d|2[0-3]):[0-5]\\d(:${t})${r}`;
}
function xZ(e) {
  return new RegExp(`^${E0(e)}$`);
}
function wZ(e) {
  let t = `${k0}T${E0(e)}`, r = [];
  if (r.push(e.local ? "Z?" : "Z"), e.offset) r.push("([+-]\\d{2}:?\\d{2})");
  return t = `${t}(${r.join("|")})`, new RegExp(`^${t}$`);
}
function kZ(e, t) {
  if ((t === "v4" || !t) && gZ.test(e)) return true;
  if ((t === "v6" || !t) && yZ.test(e)) return true;
  return false;
}
function EZ(e, t) {
  if (!dZ.test(e)) return false;
  try {
    let [r] = e.split(".");
    if (!r) return false;
    let o = r.replace(/-/g, "+").replace(/_/g, "/").padEnd(r.length + (4 - r.length % 4) % 4, "="), n = JSON.parse(atob(o));
    if (typeof n !== "object" || n === null) return false;
    if ("typ" in n && n?.typ !== "JWT") return false;
    if (!n.alg) return false;
    if (t && n.alg !== t) return false;
    return true;
  } catch {
    return false;
  }
}
function TZ(e, t) {
  if ((t === "v4" || !t) && hZ.test(e)) return true;
  if ((t === "v6" || !t) && bZ.test(e)) return true;
  return false;
}
var Zr = class _Zr extends oe {
  _parse(e) {
    if (this._def.coerce) e.data = String(e.data);
    if (this._getType(e) !== C.string) {
      let n = this._getOrReturnCtx(e);
      return j(n, { code: I.invalid_type, expected: C.string, received: n.parsedType }), W;
    }
    let r = new st(), o = void 0;
    for (let n of this._def.checks) if (n.kind === "min") {
      if (e.data.length < n.value) o = this._getOrReturnCtx(e, o), j(o, { code: I.too_small, minimum: n.value, type: "string", inclusive: true, exact: false, message: n.message }), r.dirty();
    } else if (n.kind === "max") {
      if (e.data.length > n.value) o = this._getOrReturnCtx(e, o), j(o, { code: I.too_big, maximum: n.value, type: "string", inclusive: true, exact: false, message: n.message }), r.dirty();
    } else if (n.kind === "length") {
      let i = e.data.length > n.value, s = e.data.length < n.value;
      if (i || s) {
        if (o = this._getOrReturnCtx(e, o), i) j(o, { code: I.too_big, maximum: n.value, type: "string", inclusive: true, exact: true, message: n.message });
        else if (s) j(o, { code: I.too_small, minimum: n.value, type: "string", inclusive: true, exact: true, message: n.message });
        r.dirty();
      }
    } else if (n.kind === "email") {
      if (!fZ.test(e.data)) o = this._getOrReturnCtx(e, o), j(o, { validation: "email", code: I.invalid_string, message: n.message }), r.dirty();
    } else if (n.kind === "emoji") {
      if (!Ky) Ky = new RegExp(mZ, "u");
      if (!Ky.test(e.data)) o = this._getOrReturnCtx(e, o), j(o, { validation: "emoji", code: I.invalid_string, message: n.message }), r.dirty();
    } else if (n.kind === "uuid") {
      if (!lZ.test(e.data)) o = this._getOrReturnCtx(e, o), j(o, { validation: "uuid", code: I.invalid_string, message: n.message }), r.dirty();
    } else if (n.kind === "nanoid") {
      if (!uZ.test(e.data)) o = this._getOrReturnCtx(e, o), j(o, { validation: "nanoid", code: I.invalid_string, message: n.message }), r.dirty();
    } else if (n.kind === "cuid") {
      if (!sZ.test(e.data)) o = this._getOrReturnCtx(e, o), j(o, { validation: "cuid", code: I.invalid_string, message: n.message }), r.dirty();
    } else if (n.kind === "cuid2") {
      if (!aZ.test(e.data)) o = this._getOrReturnCtx(e, o), j(o, { validation: "cuid2", code: I.invalid_string, message: n.message }), r.dirty();
    } else if (n.kind === "ulid") {
      if (!cZ.test(e.data)) o = this._getOrReturnCtx(e, o), j(o, { validation: "ulid", code: I.invalid_string, message: n.message }), r.dirty();
    } else if (n.kind === "url") try {
      new URL(e.data);
    } catch {
      o = this._getOrReturnCtx(e, o), j(o, { validation: "url", code: I.invalid_string, message: n.message }), r.dirty();
    }
    else if (n.kind === "regex") {
      if (n.regex.lastIndex = 0, !n.regex.test(e.data)) o = this._getOrReturnCtx(e, o), j(o, { validation: "regex", code: I.invalid_string, message: n.message }), r.dirty();
    } else if (n.kind === "trim") e.data = e.data.trim();
    else if (n.kind === "includes") {
      if (!e.data.includes(n.value, n.position)) o = this._getOrReturnCtx(e, o), j(o, { code: I.invalid_string, validation: { includes: n.value, position: n.position }, message: n.message }), r.dirty();
    } else if (n.kind === "toLowerCase") e.data = e.data.toLowerCase();
    else if (n.kind === "toUpperCase") e.data = e.data.toUpperCase();
    else if (n.kind === "startsWith") {
      if (!e.data.startsWith(n.value)) o = this._getOrReturnCtx(e, o), j(o, { code: I.invalid_string, validation: { startsWith: n.value }, message: n.message }), r.dirty();
    } else if (n.kind === "endsWith") {
      if (!e.data.endsWith(n.value)) o = this._getOrReturnCtx(e, o), j(o, { code: I.invalid_string, validation: { endsWith: n.value }, message: n.message }), r.dirty();
    } else if (n.kind === "datetime") {
      if (!wZ(n).test(e.data)) o = this._getOrReturnCtx(e, o), j(o, { code: I.invalid_string, validation: "datetime", message: n.message }), r.dirty();
    } else if (n.kind === "date") {
      if (!SZ.test(e.data)) o = this._getOrReturnCtx(e, o), j(o, { code: I.invalid_string, validation: "date", message: n.message }), r.dirty();
    } else if (n.kind === "time") {
      if (!xZ(n).test(e.data)) o = this._getOrReturnCtx(e, o), j(o, { code: I.invalid_string, validation: "time", message: n.message }), r.dirty();
    } else if (n.kind === "duration") {
      if (!pZ.test(e.data)) o = this._getOrReturnCtx(e, o), j(o, { validation: "duration", code: I.invalid_string, message: n.message }), r.dirty();
    } else if (n.kind === "ip") {
      if (!kZ(e.data, n.version)) o = this._getOrReturnCtx(e, o), j(o, { validation: "ip", code: I.invalid_string, message: n.message }), r.dirty();
    } else if (n.kind === "jwt") {
      if (!EZ(e.data, n.alg)) o = this._getOrReturnCtx(e, o), j(o, { validation: "jwt", code: I.invalid_string, message: n.message }), r.dirty();
    } else if (n.kind === "cidr") {
      if (!TZ(e.data, n.version)) o = this._getOrReturnCtx(e, o), j(o, { validation: "cidr", code: I.invalid_string, message: n.message }), r.dirty();
    } else if (n.kind === "base64") {
      if (!_Z.test(e.data)) o = this._getOrReturnCtx(e, o), j(o, { validation: "base64", code: I.invalid_string, message: n.message }), r.dirty();
    } else if (n.kind === "base64url") {
      if (!vZ.test(e.data)) o = this._getOrReturnCtx(e, o), j(o, { validation: "base64url", code: I.invalid_string, message: n.message }), r.dirty();
    } else ae.assertNever(n);
    return { status: r.value, value: e.data };
  }
  _regex(e, t, r) {
    return this.refinement((o) => e.test(o), { validation: t, code: I.invalid_string, ...F.errToObj(r) });
  }
  _addCheck(e) {
    return new _Zr({ ...this._def, checks: [...this._def.checks, e] });
  }
  email(e) {
    return this._addCheck({ kind: "email", ...F.errToObj(e) });
  }
  url(e) {
    return this._addCheck({ kind: "url", ...F.errToObj(e) });
  }
  emoji(e) {
    return this._addCheck({ kind: "emoji", ...F.errToObj(e) });
  }
  uuid(e) {
    return this._addCheck({ kind: "uuid", ...F.errToObj(e) });
  }
  nanoid(e) {
    return this._addCheck({ kind: "nanoid", ...F.errToObj(e) });
  }
  cuid(e) {
    return this._addCheck({ kind: "cuid", ...F.errToObj(e) });
  }
  cuid2(e) {
    return this._addCheck({ kind: "cuid2", ...F.errToObj(e) });
  }
  ulid(e) {
    return this._addCheck({ kind: "ulid", ...F.errToObj(e) });
  }
  base64(e) {
    return this._addCheck({ kind: "base64", ...F.errToObj(e) });
  }
  base64url(e) {
    return this._addCheck({ kind: "base64url", ...F.errToObj(e) });
  }
  jwt(e) {
    return this._addCheck({ kind: "jwt", ...F.errToObj(e) });
  }
  ip(e) {
    return this._addCheck({ kind: "ip", ...F.errToObj(e) });
  }
  cidr(e) {
    return this._addCheck({ kind: "cidr", ...F.errToObj(e) });
  }
  datetime(e) {
    if (typeof e === "string") return this._addCheck({ kind: "datetime", precision: null, offset: false, local: false, message: e });
    return this._addCheck({ kind: "datetime", precision: typeof e?.precision > "u" ? null : e?.precision, offset: e?.offset ?? false, local: e?.local ?? false, ...F.errToObj(e?.message) });
  }
  date(e) {
    return this._addCheck({ kind: "date", message: e });
  }
  time(e) {
    if (typeof e === "string") return this._addCheck({ kind: "time", precision: null, message: e });
    return this._addCheck({ kind: "time", precision: typeof e?.precision > "u" ? null : e?.precision, ...F.errToObj(e?.message) });
  }
  duration(e) {
    return this._addCheck({ kind: "duration", ...F.errToObj(e) });
  }
  regex(e, t) {
    return this._addCheck({ kind: "regex", regex: e, ...F.errToObj(t) });
  }
  includes(e, t) {
    return this._addCheck({ kind: "includes", value: e, position: t?.position, ...F.errToObj(t?.message) });
  }
  startsWith(e, t) {
    return this._addCheck({ kind: "startsWith", value: e, ...F.errToObj(t) });
  }
  endsWith(e, t) {
    return this._addCheck({ kind: "endsWith", value: e, ...F.errToObj(t) });
  }
  min(e, t) {
    return this._addCheck({ kind: "min", value: e, ...F.errToObj(t) });
  }
  max(e, t) {
    return this._addCheck({ kind: "max", value: e, ...F.errToObj(t) });
  }
  length(e, t) {
    return this._addCheck({ kind: "length", value: e, ...F.errToObj(t) });
  }
  nonempty(e) {
    return this.min(1, F.errToObj(e));
  }
  trim() {
    return new _Zr({ ...this._def, checks: [...this._def.checks, { kind: "trim" }] });
  }
  toLowerCase() {
    return new _Zr({ ...this._def, checks: [...this._def.checks, { kind: "toLowerCase" }] });
  }
  toUpperCase() {
    return new _Zr({ ...this._def, checks: [...this._def.checks, { kind: "toUpperCase" }] });
  }
  get isDatetime() {
    return !!this._def.checks.find((e) => e.kind === "datetime");
  }
  get isDate() {
    return !!this._def.checks.find((e) => e.kind === "date");
  }
  get isTime() {
    return !!this._def.checks.find((e) => e.kind === "time");
  }
  get isDuration() {
    return !!this._def.checks.find((e) => e.kind === "duration");
  }
  get isEmail() {
    return !!this._def.checks.find((e) => e.kind === "email");
  }
  get isURL() {
    return !!this._def.checks.find((e) => e.kind === "url");
  }
  get isEmoji() {
    return !!this._def.checks.find((e) => e.kind === "emoji");
  }
  get isUUID() {
    return !!this._def.checks.find((e) => e.kind === "uuid");
  }
  get isNANOID() {
    return !!this._def.checks.find((e) => e.kind === "nanoid");
  }
  get isCUID() {
    return !!this._def.checks.find((e) => e.kind === "cuid");
  }
  get isCUID2() {
    return !!this._def.checks.find((e) => e.kind === "cuid2");
  }
  get isULID() {
    return !!this._def.checks.find((e) => e.kind === "ulid");
  }
  get isIP() {
    return !!this._def.checks.find((e) => e.kind === "ip");
  }
  get isCIDR() {
    return !!this._def.checks.find((e) => e.kind === "cidr");
  }
  get isBase64() {
    return !!this._def.checks.find((e) => e.kind === "base64");
  }
  get isBase64url() {
    return !!this._def.checks.find((e) => e.kind === "base64url");
  }
  get minLength() {
    let e = null;
    for (let t of this._def.checks) if (t.kind === "min") {
      if (e === null || t.value > e) e = t.value;
    }
    return e;
  }
  get maxLength() {
    let e = null;
    for (let t of this._def.checks) if (t.kind === "max") {
      if (e === null || t.value < e) e = t.value;
    }
    return e;
  }
};
Zr.create = (e) => new Zr({ checks: [], typeName: R.ZodString, coerce: e?.coerce ?? false, ...Q(e) });
function PZ(e, t) {
  let r = (e.toString().split(".")[1] || "").length, o = (t.toString().split(".")[1] || "").length, n = r > o ? r : o, i = Number.parseInt(e.toFixed(n).replace(".", "")), s = Number.parseInt(t.toFixed(n).replace(".", ""));
  return i % s / 10 ** n;
}
var Li = class _Li extends oe {
  constructor() {
    super(...arguments);
    this.min = this.gte, this.max = this.lte, this.step = this.multipleOf;
  }
  _parse(e) {
    if (this._def.coerce) e.data = Number(e.data);
    if (this._getType(e) !== C.number) {
      let n = this._getOrReturnCtx(e);
      return j(n, { code: I.invalid_type, expected: C.number, received: n.parsedType }), W;
    }
    let r = void 0, o = new st();
    for (let n of this._def.checks) if (n.kind === "int") {
      if (!ae.isInteger(e.data)) r = this._getOrReturnCtx(e, r), j(r, { code: I.invalid_type, expected: "integer", received: "float", message: n.message }), o.dirty();
    } else if (n.kind === "min") {
      if (n.inclusive ? e.data < n.value : e.data <= n.value) r = this._getOrReturnCtx(e, r), j(r, { code: I.too_small, minimum: n.value, type: "number", inclusive: n.inclusive, exact: false, message: n.message }), o.dirty();
    } else if (n.kind === "max") {
      if (n.inclusive ? e.data > n.value : e.data >= n.value) r = this._getOrReturnCtx(e, r), j(r, { code: I.too_big, maximum: n.value, type: "number", inclusive: n.inclusive, exact: false, message: n.message }), o.dirty();
    } else if (n.kind === "multipleOf") {
      if (PZ(e.data, n.value) !== 0) r = this._getOrReturnCtx(e, r), j(r, { code: I.not_multiple_of, multipleOf: n.value, message: n.message }), o.dirty();
    } else if (n.kind === "finite") {
      if (!Number.isFinite(e.data)) r = this._getOrReturnCtx(e, r), j(r, { code: I.not_finite, message: n.message }), o.dirty();
    } else ae.assertNever(n);
    return { status: o.value, value: e.data };
  }
  gte(e, t) {
    return this.setLimit("min", e, true, F.toString(t));
  }
  gt(e, t) {
    return this.setLimit("min", e, false, F.toString(t));
  }
  lte(e, t) {
    return this.setLimit("max", e, true, F.toString(t));
  }
  lt(e, t) {
    return this.setLimit("max", e, false, F.toString(t));
  }
  setLimit(e, t, r, o) {
    return new _Li({ ...this._def, checks: [...this._def.checks, { kind: e, value: t, inclusive: r, message: F.toString(o) }] });
  }
  _addCheck(e) {
    return new _Li({ ...this._def, checks: [...this._def.checks, e] });
  }
  int(e) {
    return this._addCheck({ kind: "int", message: F.toString(e) });
  }
  positive(e) {
    return this._addCheck({ kind: "min", value: 0, inclusive: false, message: F.toString(e) });
  }
  negative(e) {
    return this._addCheck({ kind: "max", value: 0, inclusive: false, message: F.toString(e) });
  }
  nonpositive(e) {
    return this._addCheck({ kind: "max", value: 0, inclusive: true, message: F.toString(e) });
  }
  nonnegative(e) {
    return this._addCheck({ kind: "min", value: 0, inclusive: true, message: F.toString(e) });
  }
  multipleOf(e, t) {
    return this._addCheck({ kind: "multipleOf", value: e, message: F.toString(t) });
  }
  finite(e) {
    return this._addCheck({ kind: "finite", message: F.toString(e) });
  }
  safe(e) {
    return this._addCheck({ kind: "min", inclusive: true, value: Number.MIN_SAFE_INTEGER, message: F.toString(e) })._addCheck({ kind: "max", inclusive: true, value: Number.MAX_SAFE_INTEGER, message: F.toString(e) });
  }
  get minValue() {
    let e = null;
    for (let t of this._def.checks) if (t.kind === "min") {
      if (e === null || t.value > e) e = t.value;
    }
    return e;
  }
  get maxValue() {
    let e = null;
    for (let t of this._def.checks) if (t.kind === "max") {
      if (e === null || t.value < e) e = t.value;
    }
    return e;
  }
  get isInt() {
    return !!this._def.checks.find((e) => e.kind === "int" || e.kind === "multipleOf" && ae.isInteger(e.value));
  }
  get isFinite() {
    let e = null, t = null;
    for (let r of this._def.checks) if (r.kind === "finite" || r.kind === "int" || r.kind === "multipleOf") return true;
    else if (r.kind === "min") {
      if (t === null || r.value > t) t = r.value;
    } else if (r.kind === "max") {
      if (e === null || r.value < e) e = r.value;
    }
    return Number.isFinite(t) && Number.isFinite(e);
  }
};
Li.create = (e) => new Li({ checks: [], typeName: R.ZodNumber, coerce: e?.coerce || false, ...Q(e) });
var Fi = class _Fi extends oe {
  constructor() {
    super(...arguments);
    this.min = this.gte, this.max = this.lte;
  }
  _parse(e) {
    if (this._def.coerce) try {
      e.data = BigInt(e.data);
    } catch {
      return this._getInvalidInput(e);
    }
    if (this._getType(e) !== C.bigint) return this._getInvalidInput(e);
    let r = void 0, o = new st();
    for (let n of this._def.checks) if (n.kind === "min") {
      if (n.inclusive ? e.data < n.value : e.data <= n.value) r = this._getOrReturnCtx(e, r), j(r, { code: I.too_small, type: "bigint", minimum: n.value, inclusive: n.inclusive, message: n.message }), o.dirty();
    } else if (n.kind === "max") {
      if (n.inclusive ? e.data > n.value : e.data >= n.value) r = this._getOrReturnCtx(e, r), j(r, { code: I.too_big, type: "bigint", maximum: n.value, inclusive: n.inclusive, message: n.message }), o.dirty();
    } else if (n.kind === "multipleOf") {
      if (e.data % n.value !== BigInt(0)) r = this._getOrReturnCtx(e, r), j(r, { code: I.not_multiple_of, multipleOf: n.value, message: n.message }), o.dirty();
    } else ae.assertNever(n);
    return { status: o.value, value: e.data };
  }
  _getInvalidInput(e) {
    let t = this._getOrReturnCtx(e);
    return j(t, { code: I.invalid_type, expected: C.bigint, received: t.parsedType }), W;
  }
  gte(e, t) {
    return this.setLimit("min", e, true, F.toString(t));
  }
  gt(e, t) {
    return this.setLimit("min", e, false, F.toString(t));
  }
  lte(e, t) {
    return this.setLimit("max", e, true, F.toString(t));
  }
  lt(e, t) {
    return this.setLimit("max", e, false, F.toString(t));
  }
  setLimit(e, t, r, o) {
    return new _Fi({ ...this._def, checks: [...this._def.checks, { kind: e, value: t, inclusive: r, message: F.toString(o) }] });
  }
  _addCheck(e) {
    return new _Fi({ ...this._def, checks: [...this._def.checks, e] });
  }
  positive(e) {
    return this._addCheck({ kind: "min", value: BigInt(0), inclusive: false, message: F.toString(e) });
  }
  negative(e) {
    return this._addCheck({ kind: "max", value: BigInt(0), inclusive: false, message: F.toString(e) });
  }
  nonpositive(e) {
    return this._addCheck({ kind: "max", value: BigInt(0), inclusive: true, message: F.toString(e) });
  }
  nonnegative(e) {
    return this._addCheck({ kind: "min", value: BigInt(0), inclusive: true, message: F.toString(e) });
  }
  multipleOf(e, t) {
    return this._addCheck({ kind: "multipleOf", value: e, message: F.toString(t) });
  }
  get minValue() {
    let e = null;
    for (let t of this._def.checks) if (t.kind === "min") {
      if (e === null || t.value > e) e = t.value;
    }
    return e;
  }
  get maxValue() {
    let e = null;
    for (let t of this._def.checks) if (t.kind === "max") {
      if (e === null || t.value < e) e = t.value;
    }
    return e;
  }
};
Fi.create = (e) => new Fi({ checks: [], typeName: R.ZodBigInt, coerce: e?.coerce ?? false, ...Q(e) });
var jd = class extends oe {
  _parse(e) {
    if (this._def.coerce) e.data = Boolean(e.data);
    if (this._getType(e) !== C.boolean) {
      let r = this._getOrReturnCtx(e);
      return j(r, { code: I.invalid_type, expected: C.boolean, received: r.parsedType }), W;
    }
    return pt(e.data);
  }
};
jd.create = (e) => new jd({ typeName: R.ZodBoolean, coerce: e?.coerce || false, ...Q(e) });
var ic = class _ic extends oe {
  _parse(e) {
    if (this._def.coerce) e.data = new Date(e.data);
    if (this._getType(e) !== C.date) {
      let n = this._getOrReturnCtx(e);
      return j(n, { code: I.invalid_type, expected: C.date, received: n.parsedType }), W;
    }
    if (Number.isNaN(e.data.getTime())) {
      let n = this._getOrReturnCtx(e);
      return j(n, { code: I.invalid_date }), W;
    }
    let r = new st(), o = void 0;
    for (let n of this._def.checks) if (n.kind === "min") {
      if (e.data.getTime() < n.value) o = this._getOrReturnCtx(e, o), j(o, { code: I.too_small, message: n.message, inclusive: true, exact: false, minimum: n.value, type: "date" }), r.dirty();
    } else if (n.kind === "max") {
      if (e.data.getTime() > n.value) o = this._getOrReturnCtx(e, o), j(o, { code: I.too_big, message: n.message, inclusive: true, exact: false, maximum: n.value, type: "date" }), r.dirty();
    } else ae.assertNever(n);
    return { status: r.value, value: new Date(e.data.getTime()) };
  }
  _addCheck(e) {
    return new _ic({ ...this._def, checks: [...this._def.checks, e] });
  }
  min(e, t) {
    return this._addCheck({ kind: "min", value: e.getTime(), message: F.toString(t) });
  }
  max(e, t) {
    return this._addCheck({ kind: "max", value: e.getTime(), message: F.toString(t) });
  }
  get minDate() {
    let e = null;
    for (let t of this._def.checks) if (t.kind === "min") {
      if (e === null || t.value > e) e = t.value;
    }
    return e != null ? new Date(e) : null;
  }
  get maxDate() {
    let e = null;
    for (let t of this._def.checks) if (t.kind === "max") {
      if (e === null || t.value < e) e = t.value;
    }
    return e != null ? new Date(e) : null;
  }
};
ic.create = (e) => new ic({ checks: [], coerce: e?.coerce || false, typeName: R.ZodDate, ...Q(e) });
var Ud = class extends oe {
  _parse(e) {
    if (this._getType(e) !== C.symbol) {
      let r = this._getOrReturnCtx(e);
      return j(r, { code: I.invalid_type, expected: C.symbol, received: r.parsedType }), W;
    }
    return pt(e.data);
  }
};
Ud.create = (e) => new Ud({ typeName: R.ZodSymbol, ...Q(e) });
var sc = class extends oe {
  _parse(e) {
    if (this._getType(e) !== C.undefined) {
      let r = this._getOrReturnCtx(e);
      return j(r, { code: I.invalid_type, expected: C.undefined, received: r.parsedType }), W;
    }
    return pt(e.data);
  }
};
sc.create = (e) => new sc({ typeName: R.ZodUndefined, ...Q(e) });
var ac = class extends oe {
  _parse(e) {
    if (this._getType(e) !== C.null) {
      let r = this._getOrReturnCtx(e);
      return j(r, { code: I.invalid_type, expected: C.null, received: r.parsedType }), W;
    }
    return pt(e.data);
  }
};
ac.create = (e) => new ac({ typeName: R.ZodNull, ...Q(e) });
var zd = class extends oe {
  constructor() {
    super(...arguments);
    this._any = true;
  }
  _parse(e) {
    return pt(e.data);
  }
};
zd.create = (e) => new zd({ typeName: R.ZodAny, ...Q(e) });
var wo = class extends oe {
  constructor() {
    super(...arguments);
    this._unknown = true;
  }
  _parse(e) {
    return pt(e.data);
  }
};
wo.create = (e) => new wo({ typeName: R.ZodUnknown, ...Q(e) });
var Vr = class extends oe {
  _parse(e) {
    let t = this._getOrReturnCtx(e);
    return j(t, { code: I.invalid_type, expected: C.never, received: t.parsedType }), W;
  }
};
Vr.create = (e) => new Vr({ typeName: R.ZodNever, ...Q(e) });
var Ld = class extends oe {
  _parse(e) {
    if (this._getType(e) !== C.undefined) {
      let r = this._getOrReturnCtx(e);
      return j(r, { code: I.invalid_type, expected: C.void, received: r.parsedType }), W;
    }
    return pt(e.data);
  }
};
Ld.create = (e) => new Ld({ typeName: R.ZodVoid, ...Q(e) });
var Er = class _Er extends oe {
  _parse(e) {
    let { ctx: t, status: r } = this._processInputParams(e), o = this._def;
    if (t.parsedType !== C.array) return j(t, { code: I.invalid_type, expected: C.array, received: t.parsedType }), W;
    if (o.exactLength !== null) {
      let i = t.data.length > o.exactLength.value, s = t.data.length < o.exactLength.value;
      if (i || s) j(t, { code: i ? I.too_big : I.too_small, minimum: s ? o.exactLength.value : void 0, maximum: i ? o.exactLength.value : void 0, type: "array", inclusive: true, exact: true, message: o.exactLength.message }), r.dirty();
    }
    if (o.minLength !== null) {
      if (t.data.length < o.minLength.value) j(t, { code: I.too_small, minimum: o.minLength.value, type: "array", inclusive: true, exact: false, message: o.minLength.message }), r.dirty();
    }
    if (o.maxLength !== null) {
      if (t.data.length > o.maxLength.value) j(t, { code: I.too_big, maximum: o.maxLength.value, type: "array", inclusive: true, exact: false, message: o.maxLength.message }), r.dirty();
    }
    if (t.common.async) return Promise.all([...t.data].map((i, s) => o.type._parseAsync(new lr(t, i, t.path, s)))).then((i) => st.mergeArray(r, i));
    let n = [...t.data].map((i, s) => o.type._parseSync(new lr(t, i, t.path, s)));
    return st.mergeArray(r, n);
  }
  get element() {
    return this._def.type;
  }
  min(e, t) {
    return new _Er({ ...this._def, minLength: { value: e, message: F.toString(t) } });
  }
  max(e, t) {
    return new _Er({ ...this._def, maxLength: { value: e, message: F.toString(t) } });
  }
  length(e, t) {
    return new _Er({ ...this._def, exactLength: { value: e, message: F.toString(t) } });
  }
  nonempty(e) {
    return this.min(1, e);
  }
};
Er.create = (e, t) => new Er({ type: e, minLength: null, maxLength: null, exactLength: null, typeName: R.ZodArray, ...Q(t) });
function zi(e) {
  if (e instanceof ze) {
    let t = {};
    for (let r in e.shape) {
      let o = e.shape[r];
      t[r] = Gt.create(zi(o));
    }
    return new ze({ ...e._def, shape: () => t });
  } else if (e instanceof Er) return new Er({ ...e._def, type: zi(e.element) });
  else if (e instanceof Gt) return Gt.create(zi(e.unwrap()));
  else if (e instanceof Tn) return Tn.create(zi(e.unwrap()));
  else if (e instanceof Wr) return Wr.create(e.items.map((t) => zi(t)));
  else return e;
}
var ze = class _ze extends oe {
  constructor() {
    super(...arguments);
    this._cached = null, this.nonstrict = this.passthrough, this.augment = this.extend;
  }
  _getCached() {
    if (this._cached !== null) return this._cached;
    let e = this._def.shape(), t = ae.objectKeys(e);
    return this._cached = { shape: e, keys: t }, this._cached;
  }
  _parse(e) {
    if (this._getType(e) !== C.object) {
      let c = this._getOrReturnCtx(e);
      return j(c, { code: I.invalid_type, expected: C.object, received: c.parsedType }), W;
    }
    let { status: r, ctx: o } = this._processInputParams(e), { shape: n, keys: i } = this._getCached(), s = [];
    if (!(this._def.catchall instanceof Vr && this._def.unknownKeys === "strip")) {
      for (let c in o.data) if (!i.includes(c)) s.push(c);
    }
    let a = [];
    for (let c of i) {
      let u = n[c], d = o.data[c];
      a.push({ key: { status: "valid", value: c }, value: u._parse(new lr(o, d, o.path, c)), alwaysSet: c in o.data });
    }
    if (this._def.catchall instanceof Vr) {
      let c = this._def.unknownKeys;
      if (c === "passthrough") for (let u of s) a.push({ key: { status: "valid", value: u }, value: { status: "valid", value: o.data[u] } });
      else if (c === "strict") {
        if (s.length > 0) j(o, { code: I.unrecognized_keys, keys: s }), r.dirty();
      } else if (c === "strip") ;
      else throw Error("Internal ZodObject error: invalid unknownKeys value.");
    } else {
      let c = this._def.catchall;
      for (let u of s) {
        let d = o.data[u];
        a.push({ key: { status: "valid", value: u }, value: c._parse(new lr(o, d, o.path, u)), alwaysSet: u in o.data });
      }
    }
    if (o.common.async) return Promise.resolve().then(async () => {
      let c = [];
      for (let u of a) {
        let d = await u.key, p = await u.value;
        c.push({ key: d, value: p, alwaysSet: u.alwaysSet });
      }
      return c;
    }).then((c) => st.mergeObjectSync(r, c));
    else return st.mergeObjectSync(r, a);
  }
  get shape() {
    return this._def.shape();
  }
  strict(e) {
    return F.errToObj, new _ze({ ...this._def, unknownKeys: "strict", ...e !== void 0 ? { errorMap: (t, r) => {
      let o = this._def.errorMap?.(t, r).message ?? r.defaultError;
      if (t.code === "unrecognized_keys") return { message: F.errToObj(e).message ?? o };
      return { message: o };
    } } : {} });
  }
  strip() {
    return new _ze({ ...this._def, unknownKeys: "strip" });
  }
  passthrough() {
    return new _ze({ ...this._def, unknownKeys: "passthrough" });
  }
  extend(e) {
    return new _ze({ ...this._def, shape: () => ({ ...this._def.shape(), ...e }) });
  }
  merge(e) {
    return new _ze({ unknownKeys: e._def.unknownKeys, catchall: e._def.catchall, shape: () => ({ ...this._def.shape(), ...e._def.shape() }), typeName: R.ZodObject });
  }
  setKey(e, t) {
    return this.augment({ [e]: t });
  }
  catchall(e) {
    return new _ze({ ...this._def, catchall: e });
  }
  pick(e) {
    let t = {};
    for (let r of ae.objectKeys(e)) if (e[r] && this.shape[r]) t[r] = this.shape[r];
    return new _ze({ ...this._def, shape: () => t });
  }
  omit(e) {
    let t = {};
    for (let r of ae.objectKeys(this.shape)) if (!e[r]) t[r] = this.shape[r];
    return new _ze({ ...this._def, shape: () => t });
  }
  deepPartial() {
    return zi(this);
  }
  partial(e) {
    let t = {};
    for (let r of ae.objectKeys(this.shape)) {
      let o = this.shape[r];
      if (e && !e[r]) t[r] = o;
      else t[r] = o.optional();
    }
    return new _ze({ ...this._def, shape: () => t });
  }
  required(e) {
    let t = {};
    for (let r of ae.objectKeys(this.shape)) if (e && !e[r]) t[r] = this.shape[r];
    else {
      let n = this.shape[r];
      while (n instanceof Gt) n = n._def.innerType;
      t[r] = n;
    }
    return new _ze({ ...this._def, shape: () => t });
  }
  keyof() {
    return T0(ae.objectKeys(this.shape));
  }
};
ze.create = (e, t) => new ze({ shape: () => e, unknownKeys: "strip", catchall: Vr.create(), typeName: R.ZodObject, ...Q(t) });
ze.strictCreate = (e, t) => new ze({ shape: () => e, unknownKeys: "strict", catchall: Vr.create(), typeName: R.ZodObject, ...Q(t) });
ze.lazycreate = (e, t) => new ze({ shape: e, unknownKeys: "strip", catchall: Vr.create(), typeName: R.ZodObject, ...Q(t) });
var cc = class extends oe {
  _parse(e) {
    let { ctx: t } = this._processInputParams(e), r = this._def.options;
    function o(n) {
      for (let s of n) if (s.result.status === "valid") return s.result;
      for (let s of n) if (s.result.status === "dirty") return t.common.issues.push(...s.ctx.common.issues), s.result;
      let i = n.map((s) => new Nt(s.ctx.common.issues));
      return j(t, { code: I.invalid_union, unionErrors: i }), W;
    }
    if (t.common.async) return Promise.all(r.map(async (n) => {
      let i = { ...t, common: { ...t.common, issues: [] }, parent: null };
      return { result: await n._parseAsync({ data: t.data, path: t.path, parent: i }), ctx: i };
    })).then(o);
    else {
      let n = void 0, i = [];
      for (let a of r) {
        let c = { ...t, common: { ...t.common, issues: [] }, parent: null }, u = a._parseSync({ data: t.data, path: t.path, parent: c });
        if (u.status === "valid") return u;
        else if (u.status === "dirty" && !n) n = { result: u, ctx: c };
        if (c.common.issues.length) i.push(c.common.issues);
      }
      if (n) return t.common.issues.push(...n.ctx.common.issues), n.result;
      let s = i.map((a) => new Nt(a));
      return j(t, { code: I.invalid_union, unionErrors: s }), W;
    }
  }
  get options() {
    return this._def.options;
  }
};
cc.create = (e, t) => new cc({ options: e, typeName: R.ZodUnion, ...Q(t) });
var qr = (e) => {
  if (e instanceof uc) return qr(e.schema);
  else if (e instanceof Tr) return qr(e.innerType());
  else if (e instanceof dc) return [e.value];
  else if (e instanceof ko) return e.options;
  else if (e instanceof pc) return ae.objectValues(e.enum);
  else if (e instanceof fc) return qr(e._def.innerType);
  else if (e instanceof sc) return [void 0];
  else if (e instanceof ac) return [null];
  else if (e instanceof Gt) return [void 0, ...qr(e.unwrap())];
  else if (e instanceof Tn) return [null, ...qr(e.unwrap())];
  else if (e instanceof Xy) return qr(e.unwrap());
  else if (e instanceof gc) return qr(e.unwrap());
  else if (e instanceof mc) return qr(e._def.innerType);
  else return [];
};
var Jy = class _Jy extends oe {
  _parse(e) {
    let { ctx: t } = this._processInputParams(e);
    if (t.parsedType !== C.object) return j(t, { code: I.invalid_type, expected: C.object, received: t.parsedType }), W;
    let r = this.discriminator, o = t.data[r], n = this.optionsMap.get(o);
    if (!n) return j(t, { code: I.invalid_union_discriminator, options: Array.from(this.optionsMap.keys()), path: [r] }), W;
    if (t.common.async) return n._parseAsync({ data: t.data, path: t.path, parent: t });
    else return n._parseSync({ data: t.data, path: t.path, parent: t });
  }
  get discriminator() {
    return this._def.discriminator;
  }
  get options() {
    return this._def.options;
  }
  get optionsMap() {
    return this._def.optionsMap;
  }
  static create(e, t, r) {
    let o = /* @__PURE__ */ new Map();
    for (let n of t) {
      let i = qr(n.shape[e]);
      if (!i.length) throw Error(`A discriminator value for key \`${e}\` could not be extracted from all schema options`);
      for (let s of i) {
        if (o.has(s)) throw Error(`Discriminator property ${String(e)} has duplicate value ${String(s)}`);
        o.set(s, n);
      }
    }
    return new _Jy({ typeName: R.ZodDiscriminatedUnion, discriminator: e, options: t, optionsMap: o, ...Q(r) });
  }
};
function Gy(e, t) {
  let r = Hr(e), o = Hr(t);
  if (e === t) return { valid: true, data: e };
  else if (r === C.object && o === C.object) {
    let n = ae.objectKeys(t), i = ae.objectKeys(e).filter((a) => n.indexOf(a) !== -1), s = { ...e, ...t };
    for (let a of i) {
      let c = Gy(e[a], t[a]);
      if (!c.valid) return { valid: false };
      s[a] = c.data;
    }
    return { valid: true, data: s };
  } else if (r === C.array && o === C.array) {
    if (e.length !== t.length) return { valid: false };
    let n = [];
    for (let i = 0; i < e.length; i++) {
      let s = e[i], a = t[i], c = Gy(s, a);
      if (!c.valid) return { valid: false };
      n.push(c.data);
    }
    return { valid: true, data: n };
  } else if (r === C.date && o === C.date && +e === +t) return { valid: true, data: e };
  else return { valid: false };
}
var lc = class extends oe {
  _parse(e) {
    let { status: t, ctx: r } = this._processInputParams(e), o = (n, i) => {
      if (Vy(n) || Vy(i)) return W;
      let s = Gy(n.value, i.value);
      if (!s.valid) return j(r, { code: I.invalid_intersection_types }), W;
      if (Wy(n) || Wy(i)) t.dirty();
      return { status: t.value, value: s.data };
    };
    if (r.common.async) return Promise.all([this._def.left._parseAsync({ data: r.data, path: r.path, parent: r }), this._def.right._parseAsync({ data: r.data, path: r.path, parent: r })]).then(([n, i]) => o(n, i));
    else return o(this._def.left._parseSync({ data: r.data, path: r.path, parent: r }), this._def.right._parseSync({ data: r.data, path: r.path, parent: r }));
  }
};
lc.create = (e, t, r) => new lc({ left: e, right: t, typeName: R.ZodIntersection, ...Q(r) });
var Wr = class _Wr extends oe {
  _parse(e) {
    let { status: t, ctx: r } = this._processInputParams(e);
    if (r.parsedType !== C.array) return j(r, { code: I.invalid_type, expected: C.array, received: r.parsedType }), W;
    if (r.data.length < this._def.items.length) return j(r, { code: I.too_small, minimum: this._def.items.length, inclusive: true, exact: false, type: "array" }), W;
    if (!this._def.rest && r.data.length > this._def.items.length) j(r, { code: I.too_big, maximum: this._def.items.length, inclusive: true, exact: false, type: "array" }), t.dirty();
    let n = [...r.data].map((i, s) => {
      let a = this._def.items[s] || this._def.rest;
      if (!a) return null;
      return a._parse(new lr(r, i, r.path, s));
    }).filter((i) => !!i);
    if (r.common.async) return Promise.all(n).then((i) => st.mergeArray(t, i));
    else return st.mergeArray(t, n);
  }
  get items() {
    return this._def.items;
  }
  rest(e) {
    return new _Wr({ ...this._def, rest: e });
  }
};
Wr.create = (e, t) => {
  if (!Array.isArray(e)) throw Error("You must pass an array of schemas to z.tuple([ ... ])");
  return new Wr({ items: e, typeName: R.ZodTuple, rest: null, ...Q(t) });
};
var Fd = class _Fd extends oe {
  get keySchema() {
    return this._def.keyType;
  }
  get valueSchema() {
    return this._def.valueType;
  }
  _parse(e) {
    let { status: t, ctx: r } = this._processInputParams(e);
    if (r.parsedType !== C.object) return j(r, { code: I.invalid_type, expected: C.object, received: r.parsedType }), W;
    let o = [], n = this._def.keyType, i = this._def.valueType;
    for (let s in r.data) o.push({ key: n._parse(new lr(r, s, r.path, s)), value: i._parse(new lr(r, r.data[s], r.path, s)), alwaysSet: s in r.data });
    if (r.common.async) return st.mergeObjectAsync(t, o);
    else return st.mergeObjectSync(t, o);
  }
  get element() {
    return this._def.valueType;
  }
  static create(e, t, r) {
    if (t instanceof oe) return new _Fd({ keyType: e, valueType: t, typeName: R.ZodRecord, ...Q(r) });
    return new _Fd({ keyType: Zr.create(), valueType: e, typeName: R.ZodRecord, ...Q(t) });
  }
};
var Bd = class extends oe {
  get keySchema() {
    return this._def.keyType;
  }
  get valueSchema() {
    return this._def.valueType;
  }
  _parse(e) {
    let { status: t, ctx: r } = this._processInputParams(e);
    if (r.parsedType !== C.map) return j(r, { code: I.invalid_type, expected: C.map, received: r.parsedType }), W;
    let o = this._def.keyType, n = this._def.valueType, i = [...r.data.entries()].map(([s, a], c) => ({ key: o._parse(new lr(r, s, r.path, [c, "key"])), value: n._parse(new lr(r, a, r.path, [c, "value"])) }));
    if (r.common.async) {
      let s = /* @__PURE__ */ new Map();
      return Promise.resolve().then(async () => {
        for (let a of i) {
          let c = await a.key, u = await a.value;
          if (c.status === "aborted" || u.status === "aborted") return W;
          if (c.status === "dirty" || u.status === "dirty") t.dirty();
          s.set(c.value, u.value);
        }
        return { status: t.value, value: s };
      });
    } else {
      let s = /* @__PURE__ */ new Map();
      for (let a of i) {
        let { key: c, value: u } = a;
        if (c.status === "aborted" || u.status === "aborted") return W;
        if (c.status === "dirty" || u.status === "dirty") t.dirty();
        s.set(c.value, u.value);
      }
      return { status: t.value, value: s };
    }
  }
};
Bd.create = (e, t, r) => new Bd({ valueType: t, keyType: e, typeName: R.ZodMap, ...Q(r) });
var Bi = class _Bi extends oe {
  _parse(e) {
    let { status: t, ctx: r } = this._processInputParams(e);
    if (r.parsedType !== C.set) return j(r, { code: I.invalid_type, expected: C.set, received: r.parsedType }), W;
    let o = this._def;
    if (o.minSize !== null) {
      if (r.data.size < o.minSize.value) j(r, { code: I.too_small, minimum: o.minSize.value, type: "set", inclusive: true, exact: false, message: o.minSize.message }), t.dirty();
    }
    if (o.maxSize !== null) {
      if (r.data.size > o.maxSize.value) j(r, { code: I.too_big, maximum: o.maxSize.value, type: "set", inclusive: true, exact: false, message: o.maxSize.message }), t.dirty();
    }
    let n = this._def.valueType;
    function i(a) {
      let c = /* @__PURE__ */ new Set();
      for (let u of a) {
        if (u.status === "aborted") return W;
        if (u.status === "dirty") t.dirty();
        c.add(u.value);
      }
      return { status: t.value, value: c };
    }
    let s = [...r.data.values()].map((a, c) => n._parse(new lr(r, a, r.path, c)));
    if (r.common.async) return Promise.all(s).then((a) => i(a));
    else return i(s);
  }
  min(e, t) {
    return new _Bi({ ...this._def, minSize: { value: e, message: F.toString(t) } });
  }
  max(e, t) {
    return new _Bi({ ...this._def, maxSize: { value: e, message: F.toString(t) } });
  }
  size(e, t) {
    return this.min(e, t).max(e, t);
  }
  nonempty(e) {
    return this.min(1, e);
  }
};
Bi.create = (e, t) => new Bi({ valueType: e, minSize: null, maxSize: null, typeName: R.ZodSet, ...Q(t) });
var oc = class _oc extends oe {
  constructor() {
    super(...arguments);
    this.validate = this.implement;
  }
  _parse(e) {
    let { ctx: t } = this._processInputParams(e);
    if (t.parsedType !== C.function) return j(t, { code: I.invalid_type, expected: C.function, received: t.parsedType }), W;
    function r(s, a) {
      return Nd({ data: s, path: t.path, errorMaps: [t.common.contextualErrorMap, t.schemaErrorMap, rc(), En].filter((c) => !!c), issueData: { code: I.invalid_arguments, argumentsError: a } });
    }
    function o(s, a) {
      return Nd({ data: s, path: t.path, errorMaps: [t.common.contextualErrorMap, t.schemaErrorMap, rc(), En].filter((c) => !!c), issueData: { code: I.invalid_return_type, returnTypeError: a } });
    }
    let n = { errorMap: t.common.contextualErrorMap }, i = t.data;
    if (this._def.returns instanceof Hi) {
      let s = this;
      return pt(async function(...a) {
        let c = new Nt([]), u = await s._def.args.parseAsync(a, n).catch((f) => {
          throw c.addIssue(r(a, f)), c;
        }), d = await Reflect.apply(i, this, u);
        return await s._def.returns._def.type.parseAsync(d, n).catch((f) => {
          throw c.addIssue(o(d, f)), c;
        });
      });
    } else {
      let s = this;
      return pt(function(...a) {
        let c = s._def.args.safeParse(a, n);
        if (!c.success) throw new Nt([r(a, c.error)]);
        let u = Reflect.apply(i, this, c.data), d = s._def.returns.safeParse(u, n);
        if (!d.success) throw new Nt([o(u, d.error)]);
        return d.data;
      });
    }
  }
  parameters() {
    return this._def.args;
  }
  returnType() {
    return this._def.returns;
  }
  args(...e) {
    return new _oc({ ...this._def, args: Wr.create(e).rest(wo.create()) });
  }
  returns(e) {
    return new _oc({ ...this._def, returns: e });
  }
  implement(e) {
    return this.parse(e);
  }
  strictImplement(e) {
    return this.parse(e);
  }
  static create(e, t, r) {
    return new _oc({ args: e ? e : Wr.create([]).rest(wo.create()), returns: t || wo.create(), typeName: R.ZodFunction, ...Q(r) });
  }
};
var uc = class extends oe {
  get schema() {
    return this._def.getter();
  }
  _parse(e) {
    let { ctx: t } = this._processInputParams(e);
    return this._def.getter()._parse({ data: t.data, path: t.path, parent: t });
  }
};
uc.create = (e, t) => new uc({ getter: e, typeName: R.ZodLazy, ...Q(t) });
var dc = class extends oe {
  _parse(e) {
    if (e.data !== this._def.value) {
      let t = this._getOrReturnCtx(e);
      return j(t, { received: t.data, code: I.invalid_literal, expected: this._def.value }), W;
    }
    return { status: "valid", value: e.data };
  }
  get value() {
    return this._def.value;
  }
};
dc.create = (e, t) => new dc({ value: e, typeName: R.ZodLiteral, ...Q(t) });
function T0(e, t) {
  return new ko({ values: e, typeName: R.ZodEnum, ...Q(t) });
}
var ko = class _ko extends oe {
  _parse(e) {
    if (typeof e.data !== "string") {
      let t = this._getOrReturnCtx(e), r = this._def.values;
      return j(t, { expected: ae.joinValues(r), received: t.parsedType, code: I.invalid_type }), W;
    }
    if (!this._cache) this._cache = new Set(this._def.values);
    if (!this._cache.has(e.data)) {
      let t = this._getOrReturnCtx(e), r = this._def.values;
      return j(t, { received: t.data, code: I.invalid_enum_value, options: r }), W;
    }
    return pt(e.data);
  }
  get options() {
    return this._def.values;
  }
  get enum() {
    let e = {};
    for (let t of this._def.values) e[t] = t;
    return e;
  }
  get Values() {
    let e = {};
    for (let t of this._def.values) e[t] = t;
    return e;
  }
  get Enum() {
    let e = {};
    for (let t of this._def.values) e[t] = t;
    return e;
  }
  extract(e, t = this._def) {
    return _ko.create(e, { ...this._def, ...t });
  }
  exclude(e, t = this._def) {
    return _ko.create(this.options.filter((r) => !e.includes(r)), { ...this._def, ...t });
  }
};
ko.create = T0;
var pc = class extends oe {
  _parse(e) {
    let t = ae.getValidEnumValues(this._def.values), r = this._getOrReturnCtx(e);
    if (r.parsedType !== C.string && r.parsedType !== C.number) {
      let o = ae.objectValues(t);
      return j(r, { expected: ae.joinValues(o), received: r.parsedType, code: I.invalid_type }), W;
    }
    if (!this._cache) this._cache = new Set(ae.getValidEnumValues(this._def.values));
    if (!this._cache.has(e.data)) {
      let o = ae.objectValues(t);
      return j(r, { received: r.data, code: I.invalid_enum_value, options: o }), W;
    }
    return pt(e.data);
  }
  get enum() {
    return this._def.values;
  }
};
pc.create = (e, t) => new pc({ values: e, typeName: R.ZodNativeEnum, ...Q(t) });
var Hi = class extends oe {
  unwrap() {
    return this._def.type;
  }
  _parse(e) {
    let { ctx: t } = this._processInputParams(e);
    if (t.parsedType !== C.promise && t.common.async === false) return j(t, { code: I.invalid_type, expected: C.promise, received: t.parsedType }), W;
    let r = t.parsedType === C.promise ? t.data : Promise.resolve(t.data);
    return pt(r.then((o) => this._def.type.parseAsync(o, { path: t.path, errorMap: t.common.contextualErrorMap })));
  }
};
Hi.create = (e, t) => new Hi({ type: e, typeName: R.ZodPromise, ...Q(t) });
var Tr = class extends oe {
  innerType() {
    return this._def.schema;
  }
  sourceType() {
    return this._def.schema._def.typeName === R.ZodEffects ? this._def.schema.sourceType() : this._def.schema;
  }
  _parse(e) {
    let { status: t, ctx: r } = this._processInputParams(e), o = this._def.effect || null, n = { addIssue: (i) => {
      if (j(r, i), i.fatal) t.abort();
      else t.dirty();
    }, get path() {
      return r.path;
    } };
    if (n.addIssue = n.addIssue.bind(n), o.type === "preprocess") {
      let i = o.transform(r.data, n);
      if (r.common.async) return Promise.resolve(i).then(async (s) => {
        if (t.value === "aborted") return W;
        let a = await this._def.schema._parseAsync({ data: s, path: r.path, parent: r });
        if (a.status === "aborted") return W;
        if (a.status === "dirty") return Ui(a.value);
        if (t.value === "dirty") return Ui(a.value);
        return a;
      });
      else {
        if (t.value === "aborted") return W;
        let s = this._def.schema._parseSync({ data: i, path: r.path, parent: r });
        if (s.status === "aborted") return W;
        if (s.status === "dirty") return Ui(s.value);
        if (t.value === "dirty") return Ui(s.value);
        return s;
      }
    }
    if (o.type === "refinement") {
      let i = (s) => {
        let a = o.refinement(s, n);
        if (r.common.async) return Promise.resolve(a);
        if (a instanceof Promise) throw Error("Async refinement encountered during synchronous parse operation. Use .parseAsync instead.");
        return s;
      };
      if (r.common.async === false) {
        let s = this._def.schema._parseSync({ data: r.data, path: r.path, parent: r });
        if (s.status === "aborted") return W;
        if (s.status === "dirty") t.dirty();
        return i(s.value), { status: t.value, value: s.value };
      } else return this._def.schema._parseAsync({ data: r.data, path: r.path, parent: r }).then((s) => {
        if (s.status === "aborted") return W;
        if (s.status === "dirty") t.dirty();
        return i(s.value).then(() => ({ status: t.value, value: s.value }));
      });
    }
    if (o.type === "transform") if (r.common.async === false) {
      let i = this._def.schema._parseSync({ data: r.data, path: r.path, parent: r });
      if (!xo(i)) return W;
      let s = o.transform(i.value, n);
      if (s instanceof Promise) throw Error("Asynchronous transform encountered during synchronous parse operation. Use .parseAsync instead.");
      return { status: t.value, value: s };
    } else return this._def.schema._parseAsync({ data: r.data, path: r.path, parent: r }).then((i) => {
      if (!xo(i)) return W;
      return Promise.resolve(o.transform(i.value, n)).then((s) => ({ status: t.value, value: s }));
    });
    ae.assertNever(o);
  }
};
Tr.create = (e, t, r) => new Tr({ schema: e, typeName: R.ZodEffects, effect: t, ...Q(r) });
Tr.createWithPreprocess = (e, t, r) => new Tr({ schema: t, effect: { type: "preprocess", transform: e }, typeName: R.ZodEffects, ...Q(r) });
var Gt = class extends oe {
  _parse(e) {
    if (this._getType(e) === C.undefined) return pt(void 0);
    return this._def.innerType._parse(e);
  }
  unwrap() {
    return this._def.innerType;
  }
};
Gt.create = (e, t) => new Gt({ innerType: e, typeName: R.ZodOptional, ...Q(t) });
var Tn = class extends oe {
  _parse(e) {
    if (this._getType(e) === C.null) return pt(null);
    return this._def.innerType._parse(e);
  }
  unwrap() {
    return this._def.innerType;
  }
};
Tn.create = (e, t) => new Tn({ innerType: e, typeName: R.ZodNullable, ...Q(t) });
var fc = class extends oe {
  _parse(e) {
    let { ctx: t } = this._processInputParams(e), r = t.data;
    if (t.parsedType === C.undefined) r = this._def.defaultValue();
    return this._def.innerType._parse({ data: r, path: t.path, parent: t });
  }
  removeDefault() {
    return this._def.innerType;
  }
};
fc.create = (e, t) => new fc({ innerType: e, typeName: R.ZodDefault, defaultValue: typeof t.default === "function" ? t.default : () => t.default, ...Q(t) });
var mc = class extends oe {
  _parse(e) {
    let { ctx: t } = this._processInputParams(e), r = { ...t, common: { ...t.common, issues: [] } }, o = this._def.innerType._parse({ data: r.data, path: r.path, parent: { ...r } });
    if (nc(o)) return o.then((n) => ({ status: "valid", value: n.status === "valid" ? n.value : this._def.catchValue({ get error() {
      return new Nt(r.common.issues);
    }, input: r.data }) }));
    else return { status: "valid", value: o.status === "valid" ? o.value : this._def.catchValue({ get error() {
      return new Nt(r.common.issues);
    }, input: r.data }) };
  }
  removeCatch() {
    return this._def.innerType;
  }
};
mc.create = (e, t) => new mc({ innerType: e, typeName: R.ZodCatch, catchValue: typeof t.catch === "function" ? t.catch : () => t.catch, ...Q(t) });
var Hd = class extends oe {
  _parse(e) {
    if (this._getType(e) !== C.nan) {
      let r = this._getOrReturnCtx(e);
      return j(r, { code: I.invalid_type, expected: C.nan, received: r.parsedType }), W;
    }
    return { status: "valid", value: e.data };
  }
};
Hd.create = (e) => new Hd({ typeName: R.ZodNaN, ...Q(e) });
var Mbe = Symbol("zod_brand");
var Xy = class extends oe {
  _parse(e) {
    let { ctx: t } = this._processInputParams(e), r = t.data;
    return this._def.type._parse({ data: r, path: t.path, parent: t });
  }
  unwrap() {
    return this._def.type;
  }
};
var qd = class _qd extends oe {
  _parse(e) {
    let { status: t, ctx: r } = this._processInputParams(e);
    if (r.common.async) return (async () => {
      let n = await this._def.in._parseAsync({ data: r.data, path: r.path, parent: r });
      if (n.status === "aborted") return W;
      if (n.status === "dirty") return t.dirty(), Ui(n.value);
      else return this._def.out._parseAsync({ data: n.value, path: r.path, parent: r });
    })();
    else {
      let o = this._def.in._parseSync({ data: r.data, path: r.path, parent: r });
      if (o.status === "aborted") return W;
      if (o.status === "dirty") return t.dirty(), { status: "dirty", value: o.value };
      else return this._def.out._parseSync({ data: o.value, path: r.path, parent: r });
    }
  }
  static create(e, t) {
    return new _qd({ in: e, out: t, typeName: R.ZodPipeline });
  }
};
var gc = class extends oe {
  _parse(e) {
    let t = this._def.innerType._parse(e), r = (o) => {
      if (xo(o)) o.value = Object.freeze(o.value);
      return o;
    };
    return nc(t) ? t.then((o) => r(o)) : r(t);
  }
  unwrap() {
    return this._def.innerType;
  }
};
gc.create = (e, t) => new gc({ innerType: e, typeName: R.ZodReadonly, ...Q(t) });
var Dbe = { object: ze.lazycreate };
var R;
(function(e) {
  e.ZodString = "ZodString", e.ZodNumber = "ZodNumber", e.ZodNaN = "ZodNaN", e.ZodBigInt = "ZodBigInt", e.ZodBoolean = "ZodBoolean", e.ZodDate = "ZodDate", e.ZodSymbol = "ZodSymbol", e.ZodUndefined = "ZodUndefined", e.ZodNull = "ZodNull", e.ZodAny = "ZodAny", e.ZodUnknown = "ZodUnknown", e.ZodNever = "ZodNever", e.ZodVoid = "ZodVoid", e.ZodArray = "ZodArray", e.ZodObject = "ZodObject", e.ZodUnion = "ZodUnion", e.ZodDiscriminatedUnion = "ZodDiscriminatedUnion", e.ZodIntersection = "ZodIntersection", e.ZodTuple = "ZodTuple", e.ZodRecord = "ZodRecord", e.ZodMap = "ZodMap", e.ZodSet = "ZodSet", e.ZodFunction = "ZodFunction", e.ZodLazy = "ZodLazy", e.ZodLiteral = "ZodLiteral", e.ZodEnum = "ZodEnum", e.ZodEffects = "ZodEffects", e.ZodNativeEnum = "ZodNativeEnum", e.ZodOptional = "ZodOptional", e.ZodNullable = "ZodNullable", e.ZodDefault = "ZodDefault", e.ZodCatch = "ZodCatch", e.ZodPromise = "ZodPromise", e.ZodBranded = "ZodBranded", e.ZodPipeline = "ZodPipeline", e.ZodReadonly = "ZodReadonly";
})(R || (R = {}));
var Nbe = Zr.create;
var jbe = Li.create;
var Ube = Hd.create;
var zbe = Fi.create;
var Lbe = jd.create;
var Fbe = ic.create;
var Bbe = Ud.create;
var Hbe = sc.create;
var qbe = ac.create;
var Zbe = zd.create;
var Vbe = wo.create;
var Wbe = Vr.create;
var Kbe = Ld.create;
var Gbe = Er.create;
var P0 = ze.create;
var Jbe = ze.strictCreate;
var Xbe = cc.create;
var Ybe = Jy.create;
var Qbe = lc.create;
var e_e = Wr.create;
var t_e = Fd.create;
var r_e = Bd.create;
var n_e = Bi.create;
var o_e = oc.create;
var i_e = uc.create;
var s_e = dc.create;
var a_e = ko.create;
var c_e = pc.create;
var l_e = Hi.create;
var u_e = Tr.create;
var d_e = Gt.create;
var p_e = Tn.create;
var f_e = Tr.createWithPreprocess;
var m_e = qd.create;
var ur = {};
xr(ur, { version: () => t_, util: () => O, treeifyError: () => Kd, toJSONSchema: () => is, toDotPath: () => $0, safeParseAsync: () => Rn, safeParse: () => In, registry: () => Oc, regexes: () => $n, prettifyError: () => Gd, parseAsync: () => Io, parse: () => Po, locales: () => es, isValidJWT: () => K0, isValidBase64URL: () => W0, isValidBase64: () => a_, globalRegistry: () => kt, globalConfig: () => hc, function: () => $f, formatError: () => Ki, flattenError: () => Wi, config: () => He, clone: () => at, _xid: () => Hc, _void: () => xf, _uuidv7: () => Nc, _uuidv6: () => Dc, _uuidv4: () => Mc, _uuid: () => Cc, _url: () => jc, _uppercase: () => rl, _unknown: () => Oo, _union: () => ZV, _undefined: () => bf, _ulid: () => Bc, _uint64: () => hf, _uint32: () => pf, _tuple: () => sv, _trim: () => cl, _transform: () => eW, _toUpperCase: () => ul, _toLowerCase: () => ll, _templateLiteral: () => lW, _symbol: () => yf, _success: () => iW, _stringbool: () => If, _stringFormat: () => Rf, _string: () => of, _startsWith: () => ol, _size: () => Qc, _set: () => JV, _safeParseAsync: () => Qd, _safeParse: () => Yd, _regex: () => el, _refine: () => Pf, _record: () => KV, _readonly: () => cW, _property: () => iv, _promise: () => dW, _positive: () => tv, _pipe: () => aW, _parseAsync: () => Xd, _parse: () => Jd, _overwrite: () => Yr, _optional: () => tW, _number: () => af, _nullable: () => rW, _null: () => _f, _normalize: () => al, _nonpositive: () => nv, _nonoptional: () => oW, _nonnegative: () => ov, _never: () => Sf, _negative: () => rv, _nativeEnum: () => YV, _nanoid: () => zc, _nan: () => kf, _multipleOf: () => Ao, _minSize: () => Co, _minLength: () => Cn, _min: () => Et, _mime: () => sl, _maxSize: () => rs, _maxLength: () => ns, _max: () => Jt, _map: () => GV, _lte: () => Jt, _lt: () => Jr, _lowercase: () => tl, _literal: () => QV, _length: () => os, _lazy: () => uW, _ksuid: () => qc, _jwt: () => Yc, _isoTime: () => G_, _isoDuration: () => J_, _isoDateTime: () => W_, _isoDate: () => K_, _ipv6: () => Vc, _ipv4: () => Zc, _intersection: () => WV, _int64: () => gf, _int32: () => df, _int: () => cf, _includes: () => nl, _guid: () => ts, _gte: () => Et, _gt: () => Xr, _float64: () => uf, _float32: () => lf, _file: () => Ef, _enum: () => XV, _endsWith: () => il, _emoji: () => Uc, _email: () => Ac, _e164: () => Xc, _discriminatedUnion: () => VV, _default: () => nW, _date: () => wf, _custom: () => Tf, _cuid2: () => Fc, _cuid: () => Lc, _coercedString: () => V_, _coercedNumber: () => X_, _coercedDate: () => ev, _coercedBoolean: () => Y_, _coercedBigint: () => Q_, _cidrv6: () => Kc, _cidrv4: () => Wc, _catch: () => sW, _boolean: () => ff, _bigint: () => mf, _base64url: () => Jc, _base64: () => Gc, _array: () => dl, _any: () => vf, TimePrecision: () => sf, NEVER: () => Zd, JSONSchemaGenerator: () => Of, JSONSchema: () => Y0, Doc: () => np, $output: () => rf, $input: () => nf, $constructor: () => b, $brand: () => Vd, $ZodXID: () => gp, $ZodVoid: () => Cp, $ZodUnknown: () => $o, $ZodUnion: () => Ic, $ZodUndefined: () => Rp, $ZodUUID: () => ap, $ZodURL: () => lp, $ZodULID: () => mp, $ZodType: () => G, $ZodTuple: () => An, $ZodTransform: () => Yi, $ZodTemplateLiteral: () => Yp, $ZodSymbol: () => Ip, $ZodSuccess: () => Kp, $ZodStringFormat: () => xe, $ZodString: () => On, $ZodSet: () => zp, $ZodRegistry: () => $c, $ZodRecord: () => jp, $ZodRealError: () => Vi, $ZodReadonly: () => Xp, $ZodPromise: () => Qp, $ZodPrefault: () => Vp, $ZodPipe: () => Qi, $ZodOptional: () => Hp, $ZodObject: () => Pc, $ZodNumberFormat: () => Tp, $ZodNumber: () => Ec, $ZodNullable: () => qp, $ZodNull: () => $p, $ZodNonOptional: () => Wp, $ZodNever: () => Ap, $ZodNanoID: () => dp, $ZodNaN: () => Jp, $ZodMap: () => Up, $ZodLiteral: () => Fp, $ZodLazy: () => ef, $ZodKSUID: () => hp, $ZodJWT: () => kp, $ZodIntersection: () => Np, $ZodISOTime: () => i_, $ZodISODuration: () => s_, $ZodISODateTime: () => n_, $ZodISODate: () => o_, $ZodIPv6: () => bp, $ZodIPv4: () => yp, $ZodGUID: () => sp, $ZodFunction: () => av, $ZodFile: () => Bp, $ZodError: () => kc, $ZodEnum: () => Lp, $ZodEmoji: () => up, $ZodEmail: () => cp, $ZodE164: () => wp, $ZodDiscriminatedUnion: () => Dp, $ZodDefault: () => Zp, $ZodDate: () => Mp, $ZodCustomStringFormat: () => Ep, $ZodCustom: () => tf, $ZodCheckUpperCase: () => Kb, $ZodCheckStringFormat: () => Gi, $ZodCheckStartsWith: () => Jb, $ZodCheckSizeEquals: () => Bb, $ZodCheckRegex: () => Vb, $ZodCheckProperty: () => Yb, $ZodCheckOverwrite: () => e_, $ZodCheckNumberFormat: () => Ub, $ZodCheckMultipleOf: () => jb, $ZodCheckMinSize: () => Fb, $ZodCheckMinLength: () => qb, $ZodCheckMimeType: () => Qb, $ZodCheckMaxSize: () => Lb, $ZodCheckMaxLength: () => Hb, $ZodCheckLowerCase: () => Wb, $ZodCheckLessThan: () => tp, $ZodCheckLengthEquals: () => Zb, $ZodCheckIncludes: () => Gb, $ZodCheckGreaterThan: () => rp, $ZodCheckEndsWith: () => Xb, $ZodCheckBigIntFormat: () => zb, $ZodCheck: () => Me, $ZodCatch: () => Gp, $ZodCUID2: () => fp, $ZodCUID: () => pp, $ZodCIDRv6: () => vp, $ZodCIDRv4: () => _p, $ZodBoolean: () => Ji, $ZodBigIntFormat: () => Pp, $ZodBigInt: () => Tc, $ZodBase64URL: () => xp, $ZodBase64: () => Sp, $ZodAsyncError: () => Kr, $ZodArray: () => Xi, $ZodAny: () => Op });
var Zd = Object.freeze({ status: "aborted" });
function b(e, t, r) {
  function o(a, c) {
    var u;
    Object.defineProperty(a, "_zod", { value: a._zod ?? {}, enumerable: false }), (u = a._zod).traits ?? (u.traits = /* @__PURE__ */ new Set()), a._zod.traits.add(e), t(a, c);
    for (let d in s.prototype) if (!(d in a)) Object.defineProperty(a, d, { value: s.prototype[d].bind(a) });
    a._zod.constr = s, a._zod.def = c;
  }
  let n = r?.Parent ?? Object;
  class i extends n {
  }
  Object.defineProperty(i, "name", { value: e });
  function s(a) {
    var c;
    let u = r?.Parent ? new i() : this;
    o(u, a), (c = u._zod).deferred ?? (c.deferred = []);
    for (let d of u._zod.deferred) d();
    return u;
  }
  return Object.defineProperty(s, "init", { value: o }), Object.defineProperty(s, Symbol.hasInstance, { value: (a) => {
    if (r?.Parent && a instanceof r.Parent) return true;
    return a?._zod?.traits?.has(e);
  } }), Object.defineProperty(s, "name", { value: e }), s;
}
var Vd = Symbol("zod_brand");
var Kr = class extends Error {
  constructor() {
    super("Encountered Promise during synchronous parse. Use .parseAsync() instead.");
  }
};
var hc = {};
function He(e) {
  if (e) Object.assign(hc, e);
  return hc;
}
var O = {};
xr(O, { unwrapMessage: () => yc, stringifyPrimitive: () => D, required: () => qZ, randomString: () => DZ, propertyKeyTypes: () => Sc, promiseAllObject: () => MZ, primitiveTypes: () => nb, prefixIssues: () => wt, pick: () => zZ, partial: () => HZ, optionalKeys: () => ob, omit: () => LZ, numKeys: () => NZ, nullish: () => Pn, normalizeParams: () => $, merge: () => BZ, jsonStringifyReplacer: () => Qy, joinValues: () => T, issue: () => ab, isPlainObject: () => Zi, isObject: () => qi, getSizableOrigin: () => xc, getParsedType: () => jZ, getLengthableOrigin: () => wc, getEnumValues: () => bc, getElementAtPath: () => CZ, floatSafeRemainder: () => eb, finalizeIssue: () => jt, extend: () => FZ, escapeRegex: () => Gr, esc: () => Eo, defineLazy: () => me, createTransparentProxy: () => UZ, clone: () => at, cleanRegex: () => vc, cleanEnum: () => ZZ, captureStackTrace: () => Wd, cached: () => _c, assignProp: () => tb, assertNotEqual: () => RZ, assertNever: () => OZ, assertIs: () => $Z, assertEqual: () => IZ, assert: () => AZ, allowsEval: () => rb, aborted: () => To, NUMBER_FORMAT_RANGES: () => ib, Class: () => I0, BIGINT_FORMAT_RANGES: () => sb });
function IZ(e) {
  return e;
}
function RZ(e) {
  return e;
}
function $Z(e) {
}
function OZ(e) {
  throw Error();
}
function AZ(e) {
}
function bc(e) {
  let t = Object.values(e).filter((o) => typeof o === "number");
  return Object.entries(e).filter(([o, n]) => t.indexOf(+o) === -1).map(([o, n]) => n);
}
function T(e, t = "|") {
  return e.map((r) => D(r)).join(t);
}
function Qy(e, t) {
  if (typeof t === "bigint") return t.toString();
  return t;
}
function _c(e) {
  return { get value() {
    {
      let r = e();
      return Object.defineProperty(this, "value", { value: r }), r;
    }
    throw Error("cached value already set");
  } };
}
function Pn(e) {
  return e === null || e === void 0;
}
function vc(e) {
  let t = e.startsWith("^") ? 1 : 0, r = e.endsWith("$") ? e.length - 1 : e.length;
  return e.slice(t, r);
}
function eb(e, t) {
  let r = (e.toString().split(".")[1] || "").length, o = (t.toString().split(".")[1] || "").length, n = r > o ? r : o, i = Number.parseInt(e.toFixed(n).replace(".", "")), s = Number.parseInt(t.toFixed(n).replace(".", ""));
  return i % s / 10 ** n;
}
function me(e, t, r) {
  Object.defineProperty(e, t, { get() {
    {
      let n = r();
      return e[t] = n, n;
    }
    throw Error("cached value already set");
  }, set(n) {
    Object.defineProperty(e, t, { value: n });
  }, configurable: true });
}
function tb(e, t, r) {
  Object.defineProperty(e, t, { value: r, writable: true, enumerable: true, configurable: true });
}
function CZ(e, t) {
  if (!t) return e;
  return t.reduce((r, o) => r?.[o], e);
}
function MZ(e) {
  let t = Object.keys(e), r = t.map((o) => e[o]);
  return Promise.all(r).then((o) => {
    let n = {};
    for (let i = 0; i < t.length; i++) n[t[i]] = o[i];
    return n;
  });
}
function DZ(e = 10) {
  let r = "";
  for (let o = 0; o < e; o++) r += "abcdefghijklmnopqrstuvwxyz"[Math.floor(Math.random() * 26)];
  return r;
}
function Eo(e) {
  return JSON.stringify(e);
}
var Wd = Error.captureStackTrace ? Error.captureStackTrace : (...e) => {
};
function qi(e) {
  return typeof e === "object" && e !== null && !Array.isArray(e);
}
var rb = _c(() => {
  if (typeof navigator < "u" && navigator?.userAgent?.includes("Cloudflare")) return false;
  try {
    return new Function(""), true;
  } catch (e) {
    return false;
  }
});
function Zi(e) {
  if (qi(e) === false) return false;
  let t = e.constructor;
  if (t === void 0) return true;
  let r = t.prototype;
  if (qi(r) === false) return false;
  if (Object.prototype.hasOwnProperty.call(r, "isPrototypeOf") === false) return false;
  return true;
}
function NZ(e) {
  let t = 0;
  for (let r in e) if (Object.prototype.hasOwnProperty.call(e, r)) t++;
  return t;
}
var jZ = (e) => {
  let t = typeof e;
  switch (t) {
    case "undefined":
      return "undefined";
    case "string":
      return "string";
    case "number":
      return Number.isNaN(e) ? "nan" : "number";
    case "boolean":
      return "boolean";
    case "function":
      return "function";
    case "bigint":
      return "bigint";
    case "symbol":
      return "symbol";
    case "object":
      if (Array.isArray(e)) return "array";
      if (e === null) return "null";
      if (e.then && typeof e.then === "function" && e.catch && typeof e.catch === "function") return "promise";
      if (typeof Map < "u" && e instanceof Map) return "map";
      if (typeof Set < "u" && e instanceof Set) return "set";
      if (typeof Date < "u" && e instanceof Date) return "date";
      if (typeof File < "u" && e instanceof File) return "file";
      return "object";
    default:
      throw Error(`Unknown data type: ${t}`);
  }
};
var Sc = /* @__PURE__ */ new Set(["string", "number", "symbol"]);
var nb = /* @__PURE__ */ new Set(["string", "number", "bigint", "boolean", "symbol", "undefined"]);
function Gr(e) {
  return e.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}
function at(e, t, r) {
  let o = new e._zod.constr(t ?? e._zod.def);
  if (!t || r?.parent) o._zod.parent = e;
  return o;
}
function $(e) {
  let t = e;
  if (!t) return {};
  if (typeof t === "string") return { error: () => t };
  if (t?.message !== void 0) {
    if (t?.error !== void 0) throw Error("Cannot specify both `message` and `error` params");
    t.error = t.message;
  }
  if (delete t.message, typeof t.error === "string") return { ...t, error: () => t.error };
  return t;
}
function UZ(e) {
  let t;
  return new Proxy({}, { get(r, o, n) {
    return t ?? (t = e()), Reflect.get(t, o, n);
  }, set(r, o, n, i) {
    return t ?? (t = e()), Reflect.set(t, o, n, i);
  }, has(r, o) {
    return t ?? (t = e()), Reflect.has(t, o);
  }, deleteProperty(r, o) {
    return t ?? (t = e()), Reflect.deleteProperty(t, o);
  }, ownKeys(r) {
    return t ?? (t = e()), Reflect.ownKeys(t);
  }, getOwnPropertyDescriptor(r, o) {
    return t ?? (t = e()), Reflect.getOwnPropertyDescriptor(t, o);
  }, defineProperty(r, o, n) {
    return t ?? (t = e()), Reflect.defineProperty(t, o, n);
  } });
}
function D(e) {
  if (typeof e === "bigint") return e.toString() + "n";
  if (typeof e === "string") return `"${e}"`;
  return `${e}`;
}
function ob(e) {
  return Object.keys(e).filter((t) => e[t]._zod.optin === "optional" && e[t]._zod.optout === "optional");
}
var ib = { safeint: [Number.MIN_SAFE_INTEGER, Number.MAX_SAFE_INTEGER], int32: [-2147483648, 2147483647], uint32: [0, 4294967295], float32: [-34028234663852886e22, 34028234663852886e22], float64: [-Number.MAX_VALUE, Number.MAX_VALUE] };
var sb = { int64: [BigInt("-9223372036854775808"), BigInt("9223372036854775807")], uint64: [BigInt(0), BigInt("18446744073709551615")] };
function zZ(e, t) {
  let r = {}, o = e._zod.def;
  for (let n in t) {
    if (!(n in o.shape)) throw Error(`Unrecognized key: "${n}"`);
    if (!t[n]) continue;
    r[n] = o.shape[n];
  }
  return at(e, { ...e._zod.def, shape: r, checks: [] });
}
function LZ(e, t) {
  let r = { ...e._zod.def.shape }, o = e._zod.def;
  for (let n in t) {
    if (!(n in o.shape)) throw Error(`Unrecognized key: "${n}"`);
    if (!t[n]) continue;
    delete r[n];
  }
  return at(e, { ...e._zod.def, shape: r, checks: [] });
}
function FZ(e, t) {
  if (!Zi(t)) throw Error("Invalid input to extend: expected a plain object");
  let r = { ...e._zod.def, get shape() {
    let o = { ...e._zod.def.shape, ...t };
    return tb(this, "shape", o), o;
  }, checks: [] };
  return at(e, r);
}
function BZ(e, t) {
  return at(e, { ...e._zod.def, get shape() {
    let r = { ...e._zod.def.shape, ...t._zod.def.shape };
    return tb(this, "shape", r), r;
  }, catchall: t._zod.def.catchall, checks: [] });
}
function HZ(e, t, r) {
  let o = t._zod.def.shape, n = { ...o };
  if (r) for (let i in r) {
    if (!(i in o)) throw Error(`Unrecognized key: "${i}"`);
    if (!r[i]) continue;
    n[i] = e ? new e({ type: "optional", innerType: o[i] }) : o[i];
  }
  else for (let i in o) n[i] = e ? new e({ type: "optional", innerType: o[i] }) : o[i];
  return at(t, { ...t._zod.def, shape: n, checks: [] });
}
function qZ(e, t, r) {
  let o = t._zod.def.shape, n = { ...o };
  if (r) for (let i in r) {
    if (!(i in n)) throw Error(`Unrecognized key: "${i}"`);
    if (!r[i]) continue;
    n[i] = new e({ type: "nonoptional", innerType: o[i] });
  }
  else for (let i in o) n[i] = new e({ type: "nonoptional", innerType: o[i] });
  return at(t, { ...t._zod.def, shape: n, checks: [] });
}
function To(e, t = 0) {
  for (let r = t; r < e.issues.length; r++) if (e.issues[r]?.continue !== true) return true;
  return false;
}
function wt(e, t) {
  return t.map((r) => {
    var o;
    return (o = r).path ?? (o.path = []), r.path.unshift(e), r;
  });
}
function yc(e) {
  return typeof e === "string" ? e : e?.message;
}
function jt(e, t, r) {
  let o = { ...e, path: e.path ?? [] };
  if (!e.message) {
    let n = yc(e.inst?._zod.def?.error?.(e)) ?? yc(t?.error?.(e)) ?? yc(r.customError?.(e)) ?? yc(r.localeError?.(e)) ?? "Invalid input";
    o.message = n;
  }
  if (delete o.inst, delete o.continue, !t?.reportInput) delete o.input;
  return o;
}
function xc(e) {
  if (e instanceof Set) return "set";
  if (e instanceof Map) return "map";
  if (e instanceof File) return "file";
  return "unknown";
}
function wc(e) {
  if (Array.isArray(e)) return "array";
  if (typeof e === "string") return "string";
  return "unknown";
}
function ab(...e) {
  let [t, r, o] = e;
  if (typeof t === "string") return { message: t, code: "custom", input: r, inst: o };
  return { ...t };
}
function ZZ(e) {
  return Object.entries(e).filter(([t, r]) => Number.isNaN(Number.parseInt(t, 10))).map((t) => t[1]);
}
var I0 = class {
  constructor(...e) {
  }
};
var R0 = (e, t) => {
  e.name = "$ZodError", Object.defineProperty(e, "_zod", { value: e._zod, enumerable: false }), Object.defineProperty(e, "issues", { value: t, enumerable: false }), Object.defineProperty(e, "message", { get() {
    return JSON.stringify(t, Qy, 2);
  }, enumerable: true });
};
var kc = b("$ZodError", R0);
var Vi = b("$ZodError", R0, { Parent: Error });
function Wi(e, t = (r) => r.message) {
  let r = {}, o = [];
  for (let n of e.issues) if (n.path.length > 0) r[n.path[0]] = r[n.path[0]] || [], r[n.path[0]].push(t(n));
  else o.push(t(n));
  return { formErrors: o, fieldErrors: r };
}
function Ki(e, t) {
  let r = t || function(i) {
    return i.message;
  }, o = { _errors: [] }, n = (i) => {
    for (let s of i.issues) if (s.code === "invalid_union" && s.errors.length) s.errors.map((a) => n({ issues: a }));
    else if (s.code === "invalid_key") n({ issues: s.issues });
    else if (s.code === "invalid_element") n({ issues: s.issues });
    else if (s.path.length === 0) o._errors.push(r(s));
    else {
      let a = o, c = 0;
      while (c < s.path.length) {
        let u = s.path[c];
        if (c !== s.path.length - 1) a[u] = a[u] || { _errors: [] };
        else a[u] = a[u] || { _errors: [] }, a[u]._errors.push(r(s));
        a = a[u], c++;
      }
    }
  };
  return n(e), o;
}
function Kd(e, t) {
  let r = t || function(i) {
    return i.message;
  }, o = { errors: [] }, n = (i, s = []) => {
    var a, c;
    for (let u of i.issues) if (u.code === "invalid_union" && u.errors.length) u.errors.map((d) => n({ issues: d }, u.path));
    else if (u.code === "invalid_key") n({ issues: u.issues }, u.path);
    else if (u.code === "invalid_element") n({ issues: u.issues }, u.path);
    else {
      let d = [...s, ...u.path];
      if (d.length === 0) {
        o.errors.push(r(u));
        continue;
      }
      let p = o, f = 0;
      while (f < d.length) {
        let m = d[f], g = f === d.length - 1;
        if (typeof m === "string") p.properties ?? (p.properties = {}), (a = p.properties)[m] ?? (a[m] = { errors: [] }), p = p.properties[m];
        else p.items ?? (p.items = []), (c = p.items)[m] ?? (c[m] = { errors: [] }), p = p.items[m];
        if (g) p.errors.push(r(u));
        f++;
      }
    }
  };
  return n(e), o;
}
function $0(e) {
  let t = [];
  for (let r of e) if (typeof r === "number") t.push(`[${r}]`);
  else if (typeof r === "symbol") t.push(`[${JSON.stringify(String(r))}]`);
  else if (/[^\w$]/.test(r)) t.push(`[${JSON.stringify(r)}]`);
  else {
    if (t.length) t.push(".");
    t.push(r);
  }
  return t.join("");
}
function Gd(e) {
  let t = [], r = [...e.issues].sort((o, n) => o.path.length - n.path.length);
  for (let o of r) if (t.push(`\u2716 ${o.message}`), o.path?.length) t.push(`  \u2192 at ${$0(o.path)}`);
  return t.join(`
`);
}
var Jd = (e) => (t, r, o, n) => {
  let i = o ? Object.assign(o, { async: false }) : { async: false }, s = t._zod.run({ value: r, issues: [] }, i);
  if (s instanceof Promise) throw new Kr();
  if (s.issues.length) {
    let a = new (n?.Err ?? e)(s.issues.map((c) => jt(c, i, He())));
    throw Wd(a, n?.callee), a;
  }
  return s.value;
};
var Po = Jd(Vi);
var Xd = (e) => async (t, r, o, n) => {
  let i = o ? Object.assign(o, { async: true }) : { async: true }, s = t._zod.run({ value: r, issues: [] }, i);
  if (s instanceof Promise) s = await s;
  if (s.issues.length) {
    let a = new (n?.Err ?? e)(s.issues.map((c) => jt(c, i, He())));
    throw Wd(a, n?.callee), a;
  }
  return s.value;
};
var Io = Xd(Vi);
var Yd = (e) => (t, r, o) => {
  let n = o ? { ...o, async: false } : { async: false }, i = t._zod.run({ value: r, issues: [] }, n);
  if (i instanceof Promise) throw new Kr();
  return i.issues.length ? { success: false, error: new (e ?? kc)(i.issues.map((s) => jt(s, n, He()))) } : { success: true, data: i.value };
};
var In = Yd(Vi);
var Qd = (e) => async (t, r, o) => {
  let n = o ? Object.assign(o, { async: true }) : { async: true }, i = t._zod.run({ value: r, issues: [] }, n);
  if (i instanceof Promise) i = await i;
  return i.issues.length ? { success: false, error: new e(i.issues.map((s) => jt(s, n, He()))) } : { success: true, data: i.value };
};
var Rn = Qd(Vi);
var $n = {};
xr($n, { xid: () => db, uuid7: () => JZ, uuid6: () => GZ, uuid4: () => KZ, uuid: () => Ro, uppercase: () => Nb, unicodeEmail: () => QZ, undefined: () => Mb, ulid: () => ub, time: () => Tb, string: () => Ib, rfc5322Email: () => YZ, number: () => Ob, null: () => Cb, nanoid: () => fb, lowercase: () => Db, ksuid: () => pb, ipv6: () => _b, ipv4: () => bb, integer: () => $b, html5Email: () => XZ, hostname: () => wb, guid: () => gb, extendedDuration: () => WZ, emoji: () => yb, email: () => hb, e164: () => kb, duration: () => mb, domain: () => rV, datetime: () => Pb, date: () => Eb, cuid2: () => lb, cuid: () => cb, cidrv6: () => Sb, cidrv4: () => vb, browserEmail: () => eV, boolean: () => Ab, bigint: () => Rb, base64url: () => ep, base64: () => xb, _emoji: () => tV });
var cb = /^[cC][^\s-]{8,}$/;
var lb = /^[0-9a-z]+$/;
var ub = /^[0-9A-HJKMNP-TV-Za-hjkmnp-tv-z]{26}$/;
var db = /^[0-9a-vA-V]{20}$/;
var pb = /^[A-Za-z0-9]{27}$/;
var fb = /^[a-zA-Z0-9_-]{21}$/;
var mb = /^P(?:(\d+W)|(?!.*W)(?=\d|T\d)(\d+Y)?(\d+M)?(\d+D)?(T(?=\d)(\d+H)?(\d+M)?(\d+([.,]\d+)?S)?)?)$/;
var WZ = /^[-+]?P(?!$)(?:(?:[-+]?\d+Y)|(?:[-+]?\d+[.,]\d+Y$))?(?:(?:[-+]?\d+M)|(?:[-+]?\d+[.,]\d+M$))?(?:(?:[-+]?\d+W)|(?:[-+]?\d+[.,]\d+W$))?(?:(?:[-+]?\d+D)|(?:[-+]?\d+[.,]\d+D$))?(?:T(?=[\d+-])(?:(?:[-+]?\d+H)|(?:[-+]?\d+[.,]\d+H$))?(?:(?:[-+]?\d+M)|(?:[-+]?\d+[.,]\d+M$))?(?:[-+]?\d+(?:[.,]\d+)?S)?)??$/;
var gb = /^([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})$/;
var Ro = (e) => {
  if (!e) return /^([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-8][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}|00000000-0000-0000-0000-000000000000)$/;
  return new RegExp(`^([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-${e}[0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12})$`);
};
var KZ = Ro(4);
var GZ = Ro(6);
var JZ = Ro(7);
var hb = /^(?!\.)(?!.*\.\.)([A-Za-z0-9_'+\-\.]*)[A-Za-z0-9_+-]@([A-Za-z0-9][A-Za-z0-9\-]*\.)+[A-Za-z]{2,}$/;
var XZ = /^[a-zA-Z0-9.!#$%&'*+/=?^_`{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$/;
var YZ = /^(([^<>()\[\]\\.,;:\s@"]+(\.[^<>()\[\]\\.,;:\s@"]+)*)|(".+"))@((\[[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}])|(([a-zA-Z\-0-9]+\.)+[a-zA-Z]{2,}))$/;
var QZ = /^[^\s@"]{1,64}@[^\s@]{1,255}$/u;
var eV = /^[a-zA-Z0-9.!#$%&'*+/=?^_`{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$/;
var tV = "^(\\p{Extended_Pictographic}|\\p{Emoji_Component})+$";
function yb() {
  return new RegExp("^(\\p{Extended_Pictographic}|\\p{Emoji_Component})+$", "u");
}
var bb = /^(?:(?:25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9][0-9]|[0-9])\.){3}(?:25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9][0-9]|[0-9])$/;
var _b = /^(([0-9a-fA-F]{1,4}:){7}[0-9a-fA-F]{1,4}|::|([0-9a-fA-F]{1,4})?::([0-9a-fA-F]{1,4}:?){0,6})$/;
var vb = /^((25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9][0-9]|[0-9])\.){3}(25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9][0-9]|[0-9])\/([0-9]|[1-2][0-9]|3[0-2])$/;
var Sb = /^(([0-9a-fA-F]{1,4}:){7}[0-9a-fA-F]{1,4}|::|([0-9a-fA-F]{1,4})?::([0-9a-fA-F]{1,4}:?){0,6})\/(12[0-8]|1[01][0-9]|[1-9]?[0-9])$/;
var xb = /^$|^(?:[0-9a-zA-Z+/]{4})*(?:(?:[0-9a-zA-Z+/]{2}==)|(?:[0-9a-zA-Z+/]{3}=))?$/;
var ep = /^[A-Za-z0-9_-]*$/;
var wb = /^([a-zA-Z0-9-]+\.)*[a-zA-Z0-9-]+$/;
var rV = /^([a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]{2,}$/;
var kb = /^\+(?:[0-9]){6,14}[0-9]$/;
var O0 = "(?:(?:\\d\\d[2468][048]|\\d\\d[13579][26]|\\d\\d0[48]|[02468][048]00|[13579][26]00)-02-29|\\d{4}-(?:(?:0[13578]|1[02])-(?:0[1-9]|[12]\\d|3[01])|(?:0[469]|11)-(?:0[1-9]|[12]\\d|30)|(?:02)-(?:0[1-9]|1\\d|2[0-8])))";
var Eb = new RegExp(`^${O0}$`);
function A0(e) {
  return typeof e.precision === "number" ? e.precision === -1 ? "(?:[01]\\d|2[0-3]):[0-5]\\d" : e.precision === 0 ? "(?:[01]\\d|2[0-3]):[0-5]\\d:[0-5]\\d" : `(?:[01]\\d|2[0-3]):[0-5]\\d:[0-5]\\d\\.\\d{${e.precision}}` : "(?:[01]\\d|2[0-3]):[0-5]\\d(?::[0-5]\\d(?:\\.\\d+)?)?";
}
function Tb(e) {
  return new RegExp(`^${A0(e)}$`);
}
function Pb(e) {
  let t = A0({ precision: e.precision }), r = ["Z"];
  if (e.local) r.push("");
  if (e.offset) r.push("([+-]\\d{2}:\\d{2})");
  let o = `${t}(?:${r.join("|")})`;
  return new RegExp(`^${O0}T(?:${o})$`);
}
var Ib = (e) => {
  let t = e ? `[\\s\\S]{${e?.minimum ?? 0},${e?.maximum ?? ""}}` : "[\\s\\S]*";
  return new RegExp(`^${t}$`);
};
var Rb = /^\d+n?$/;
var $b = /^\d+$/;
var Ob = /^-?\d+(?:\.\d+)?/i;
var Ab = /true|false/i;
var Cb = /null/i;
var Mb = /undefined/i;
var Db = /^[^A-Z]*$/;
var Nb = /^[^a-z]*$/;
var Me = b("$ZodCheck", (e, t) => {
  var r;
  e._zod ?? (e._zod = {}), e._zod.def = t, (r = e._zod).onattach ?? (r.onattach = []);
});
var M0 = { number: "number", bigint: "bigint", object: "date" };
var tp = b("$ZodCheckLessThan", (e, t) => {
  Me.init(e, t);
  let r = M0[typeof t.value];
  e._zod.onattach.push((o) => {
    let n = o._zod.bag, i = (t.inclusive ? n.maximum : n.exclusiveMaximum) ?? Number.POSITIVE_INFINITY;
    if (t.value < i) if (t.inclusive) n.maximum = t.value;
    else n.exclusiveMaximum = t.value;
  }), e._zod.check = (o) => {
    if (t.inclusive ? o.value <= t.value : o.value < t.value) return;
    o.issues.push({ origin: r, code: "too_big", maximum: t.value, input: o.value, inclusive: t.inclusive, inst: e, continue: !t.abort });
  };
});
var rp = b("$ZodCheckGreaterThan", (e, t) => {
  Me.init(e, t);
  let r = M0[typeof t.value];
  e._zod.onattach.push((o) => {
    let n = o._zod.bag, i = (t.inclusive ? n.minimum : n.exclusiveMinimum) ?? Number.NEGATIVE_INFINITY;
    if (t.value > i) if (t.inclusive) n.minimum = t.value;
    else n.exclusiveMinimum = t.value;
  }), e._zod.check = (o) => {
    if (t.inclusive ? o.value >= t.value : o.value > t.value) return;
    o.issues.push({ origin: r, code: "too_small", minimum: t.value, input: o.value, inclusive: t.inclusive, inst: e, continue: !t.abort });
  };
});
var jb = b("$ZodCheckMultipleOf", (e, t) => {
  Me.init(e, t), e._zod.onattach.push((r) => {
    var o;
    (o = r._zod.bag).multipleOf ?? (o.multipleOf = t.value);
  }), e._zod.check = (r) => {
    if (typeof r.value !== typeof t.value) throw Error("Cannot mix number and bigint in multiple_of check.");
    if (typeof r.value === "bigint" ? r.value % t.value === BigInt(0) : eb(r.value, t.value) === 0) return;
    r.issues.push({ origin: typeof r.value, code: "not_multiple_of", divisor: t.value, input: r.value, inst: e, continue: !t.abort });
  };
});
var Ub = b("$ZodCheckNumberFormat", (e, t) => {
  Me.init(e, t), t.format = t.format || "float64";
  let r = t.format?.includes("int"), o = r ? "int" : "number", [n, i] = ib[t.format];
  e._zod.onattach.push((s) => {
    let a = s._zod.bag;
    if (a.format = t.format, a.minimum = n, a.maximum = i, r) a.pattern = $b;
  }), e._zod.check = (s) => {
    let a = s.value;
    if (r) {
      if (!Number.isInteger(a)) {
        s.issues.push({ expected: o, format: t.format, code: "invalid_type", input: a, inst: e });
        return;
      }
      if (!Number.isSafeInteger(a)) {
        if (a > 0) s.issues.push({ input: a, code: "too_big", maximum: Number.MAX_SAFE_INTEGER, note: "Integers must be within the safe integer range.", inst: e, origin: o, continue: !t.abort });
        else s.issues.push({ input: a, code: "too_small", minimum: Number.MIN_SAFE_INTEGER, note: "Integers must be within the safe integer range.", inst: e, origin: o, continue: !t.abort });
        return;
      }
    }
    if (a < n) s.issues.push({ origin: "number", input: a, code: "too_small", minimum: n, inclusive: true, inst: e, continue: !t.abort });
    if (a > i) s.issues.push({ origin: "number", input: a, code: "too_big", maximum: i, inst: e });
  };
});
var zb = b("$ZodCheckBigIntFormat", (e, t) => {
  Me.init(e, t);
  let [r, o] = sb[t.format];
  e._zod.onattach.push((n) => {
    let i = n._zod.bag;
    i.format = t.format, i.minimum = r, i.maximum = o;
  }), e._zod.check = (n) => {
    let i = n.value;
    if (i < r) n.issues.push({ origin: "bigint", input: i, code: "too_small", minimum: r, inclusive: true, inst: e, continue: !t.abort });
    if (i > o) n.issues.push({ origin: "bigint", input: i, code: "too_big", maximum: o, inst: e });
  };
});
var Lb = b("$ZodCheckMaxSize", (e, t) => {
  Me.init(e, t), e._zod.when = (r) => {
    let o = r.value;
    return !Pn(o) && o.size !== void 0;
  }, e._zod.onattach.push((r) => {
    let o = r._zod.bag.maximum ?? Number.POSITIVE_INFINITY;
    if (t.maximum < o) r._zod.bag.maximum = t.maximum;
  }), e._zod.check = (r) => {
    let o = r.value;
    if (o.size <= t.maximum) return;
    r.issues.push({ origin: xc(o), code: "too_big", maximum: t.maximum, input: o, inst: e, continue: !t.abort });
  };
});
var Fb = b("$ZodCheckMinSize", (e, t) => {
  Me.init(e, t), e._zod.when = (r) => {
    let o = r.value;
    return !Pn(o) && o.size !== void 0;
  }, e._zod.onattach.push((r) => {
    let o = r._zod.bag.minimum ?? Number.NEGATIVE_INFINITY;
    if (t.minimum > o) r._zod.bag.minimum = t.minimum;
  }), e._zod.check = (r) => {
    let o = r.value;
    if (o.size >= t.minimum) return;
    r.issues.push({ origin: xc(o), code: "too_small", minimum: t.minimum, input: o, inst: e, continue: !t.abort });
  };
});
var Bb = b("$ZodCheckSizeEquals", (e, t) => {
  Me.init(e, t), e._zod.when = (r) => {
    let o = r.value;
    return !Pn(o) && o.size !== void 0;
  }, e._zod.onattach.push((r) => {
    let o = r._zod.bag;
    o.minimum = t.size, o.maximum = t.size, o.size = t.size;
  }), e._zod.check = (r) => {
    let o = r.value, n = o.size;
    if (n === t.size) return;
    let i = n > t.size;
    r.issues.push({ origin: xc(o), ...i ? { code: "too_big", maximum: t.size } : { code: "too_small", minimum: t.size }, inclusive: true, exact: true, input: r.value, inst: e, continue: !t.abort });
  };
});
var Hb = b("$ZodCheckMaxLength", (e, t) => {
  Me.init(e, t), e._zod.when = (r) => {
    let o = r.value;
    return !Pn(o) && o.length !== void 0;
  }, e._zod.onattach.push((r) => {
    let o = r._zod.bag.maximum ?? Number.POSITIVE_INFINITY;
    if (t.maximum < o) r._zod.bag.maximum = t.maximum;
  }), e._zod.check = (r) => {
    let o = r.value;
    if (o.length <= t.maximum) return;
    let i = wc(o);
    r.issues.push({ origin: i, code: "too_big", maximum: t.maximum, inclusive: true, input: o, inst: e, continue: !t.abort });
  };
});
var qb = b("$ZodCheckMinLength", (e, t) => {
  Me.init(e, t), e._zod.when = (r) => {
    let o = r.value;
    return !Pn(o) && o.length !== void 0;
  }, e._zod.onattach.push((r) => {
    let o = r._zod.bag.minimum ?? Number.NEGATIVE_INFINITY;
    if (t.minimum > o) r._zod.bag.minimum = t.minimum;
  }), e._zod.check = (r) => {
    let o = r.value;
    if (o.length >= t.minimum) return;
    let i = wc(o);
    r.issues.push({ origin: i, code: "too_small", minimum: t.minimum, inclusive: true, input: o, inst: e, continue: !t.abort });
  };
});
var Zb = b("$ZodCheckLengthEquals", (e, t) => {
  Me.init(e, t), e._zod.when = (r) => {
    let o = r.value;
    return !Pn(o) && o.length !== void 0;
  }, e._zod.onattach.push((r) => {
    let o = r._zod.bag;
    o.minimum = t.length, o.maximum = t.length, o.length = t.length;
  }), e._zod.check = (r) => {
    let o = r.value, n = o.length;
    if (n === t.length) return;
    let i = wc(o), s = n > t.length;
    r.issues.push({ origin: i, ...s ? { code: "too_big", maximum: t.length } : { code: "too_small", minimum: t.length }, inclusive: true, exact: true, input: r.value, inst: e, continue: !t.abort });
  };
});
var Gi = b("$ZodCheckStringFormat", (e, t) => {
  var r, o;
  if (Me.init(e, t), e._zod.onattach.push((n) => {
    let i = n._zod.bag;
    if (i.format = t.format, t.pattern) i.patterns ?? (i.patterns = /* @__PURE__ */ new Set()), i.patterns.add(t.pattern);
  }), t.pattern) (r = e._zod).check ?? (r.check = (n) => {
    if (t.pattern.lastIndex = 0, t.pattern.test(n.value)) return;
    n.issues.push({ origin: "string", code: "invalid_format", format: t.format, input: n.value, ...t.pattern ? { pattern: t.pattern.toString() } : {}, inst: e, continue: !t.abort });
  });
  else (o = e._zod).check ?? (o.check = () => {
  });
});
var Vb = b("$ZodCheckRegex", (e, t) => {
  Gi.init(e, t), e._zod.check = (r) => {
    if (t.pattern.lastIndex = 0, t.pattern.test(r.value)) return;
    r.issues.push({ origin: "string", code: "invalid_format", format: "regex", input: r.value, pattern: t.pattern.toString(), inst: e, continue: !t.abort });
  };
});
var Wb = b("$ZodCheckLowerCase", (e, t) => {
  t.pattern ?? (t.pattern = Db), Gi.init(e, t);
});
var Kb = b("$ZodCheckUpperCase", (e, t) => {
  t.pattern ?? (t.pattern = Nb), Gi.init(e, t);
});
var Gb = b("$ZodCheckIncludes", (e, t) => {
  Me.init(e, t);
  let r = Gr(t.includes), o = new RegExp(typeof t.position === "number" ? `^.{${t.position}}${r}` : r);
  t.pattern = o, e._zod.onattach.push((n) => {
    let i = n._zod.bag;
    i.patterns ?? (i.patterns = /* @__PURE__ */ new Set()), i.patterns.add(o);
  }), e._zod.check = (n) => {
    if (n.value.includes(t.includes, t.position)) return;
    n.issues.push({ origin: "string", code: "invalid_format", format: "includes", includes: t.includes, input: n.value, inst: e, continue: !t.abort });
  };
});
var Jb = b("$ZodCheckStartsWith", (e, t) => {
  Me.init(e, t);
  let r = new RegExp(`^${Gr(t.prefix)}.*`);
  t.pattern ?? (t.pattern = r), e._zod.onattach.push((o) => {
    let n = o._zod.bag;
    n.patterns ?? (n.patterns = /* @__PURE__ */ new Set()), n.patterns.add(r);
  }), e._zod.check = (o) => {
    if (o.value.startsWith(t.prefix)) return;
    o.issues.push({ origin: "string", code: "invalid_format", format: "starts_with", prefix: t.prefix, input: o.value, inst: e, continue: !t.abort });
  };
});
var Xb = b("$ZodCheckEndsWith", (e, t) => {
  Me.init(e, t);
  let r = new RegExp(`.*${Gr(t.suffix)}$`);
  t.pattern ?? (t.pattern = r), e._zod.onattach.push((o) => {
    let n = o._zod.bag;
    n.patterns ?? (n.patterns = /* @__PURE__ */ new Set()), n.patterns.add(r);
  }), e._zod.check = (o) => {
    if (o.value.endsWith(t.suffix)) return;
    o.issues.push({ origin: "string", code: "invalid_format", format: "ends_with", suffix: t.suffix, input: o.value, inst: e, continue: !t.abort });
  };
});
function C0(e, t, r) {
  if (e.issues.length) t.issues.push(...wt(r, e.issues));
}
var Yb = b("$ZodCheckProperty", (e, t) => {
  Me.init(e, t), e._zod.check = (r) => {
    let o = t.schema._zod.run({ value: r.value[t.property], issues: [] }, {});
    if (o instanceof Promise) return o.then((n) => C0(n, r, t.property));
    C0(o, r, t.property);
    return;
  };
});
var Qb = b("$ZodCheckMimeType", (e, t) => {
  Me.init(e, t);
  let r = new Set(t.mime);
  e._zod.onattach.push((o) => {
    o._zod.bag.mime = t.mime;
  }), e._zod.check = (o) => {
    if (r.has(o.value.type)) return;
    o.issues.push({ code: "invalid_value", values: t.mime, input: o.value.type, inst: e });
  };
});
var e_ = b("$ZodCheckOverwrite", (e, t) => {
  Me.init(e, t), e._zod.check = (r) => {
    r.value = t.tx(r.value);
  };
});
var np = class {
  constructor(e = []) {
    if (this.content = [], this.indent = 0, this) this.args = e;
  }
  indented(e) {
    this.indent += 1, e(this), this.indent -= 1;
  }
  write(e) {
    if (typeof e === "function") {
      e(this, { execution: "sync" }), e(this, { execution: "async" });
      return;
    }
    let r = e.split(`
`).filter((i) => i), o = Math.min(...r.map((i) => i.length - i.trimStart().length)), n = r.map((i) => i.slice(o)).map((i) => " ".repeat(this.indent * 2) + i);
    for (let i of n) this.content.push(i);
  }
  compile() {
    let e = Function, t = this?.args, o = [...(this?.content ?? [""]).map((n) => `  ${n}`)];
    return new e(...t, o.join(`
`));
  }
};
var t_ = { major: 4, minor: 0, patch: 0 };
var G = b("$ZodType", (e, t) => {
  var r;
  e ?? (e = {}), e._zod.def = t, e._zod.bag = e._zod.bag || {}, e._zod.version = t_;
  let o = [...e._zod.def.checks ?? []];
  if (e._zod.traits.has("$ZodCheck")) o.unshift(e);
  for (let n of o) for (let i of n._zod.onattach) i(e);
  if (o.length === 0) (r = e._zod).deferred ?? (r.deferred = []), e._zod.deferred?.push(() => {
    e._zod.run = e._zod.parse;
  });
  else {
    let n = (i, s, a) => {
      let c = To(i), u;
      for (let d of s) {
        if (d._zod.when) {
          if (!d._zod.when(i)) continue;
        } else if (c) continue;
        let p = i.issues.length, f = d._zod.check(i);
        if (f instanceof Promise && a?.async === false) throw new Kr();
        if (u || f instanceof Promise) u = (u ?? Promise.resolve()).then(async () => {
          if (await f, i.issues.length === p) return;
          if (!c) c = To(i, p);
        });
        else {
          if (i.issues.length === p) continue;
          if (!c) c = To(i, p);
        }
      }
      if (u) return u.then(() => i);
      return i;
    };
    e._zod.run = (i, s) => {
      let a = e._zod.parse(i, s);
      if (a instanceof Promise) {
        if (s.async === false) throw new Kr();
        return a.then((c) => n(c, o, s));
      }
      return n(a, o, s);
    };
  }
  e["~standard"] = { validate: (n) => {
    try {
      let i = In(e, n);
      return i.success ? { value: i.data } : { issues: i.error?.issues };
    } catch (i) {
      return Rn(e, n).then((s) => s.success ? { value: s.data } : { issues: s.error?.issues });
    }
  }, vendor: "zod", version: 1 };
});
var On = b("$ZodString", (e, t) => {
  G.init(e, t), e._zod.pattern = [...e?._zod.bag?.patterns ?? []].pop() ?? Ib(e._zod.bag), e._zod.parse = (r, o) => {
    if (t.coerce) try {
      r.value = String(r.value);
    } catch (n) {
    }
    if (typeof r.value === "string") return r;
    return r.issues.push({ expected: "string", code: "invalid_type", input: r.value, inst: e }), r;
  };
});
var xe = b("$ZodStringFormat", (e, t) => {
  Gi.init(e, t), On.init(e, t);
});
var sp = b("$ZodGUID", (e, t) => {
  t.pattern ?? (t.pattern = gb), xe.init(e, t);
});
var ap = b("$ZodUUID", (e, t) => {
  if (t.version) {
    let o = { v1: 1, v2: 2, v3: 3, v4: 4, v5: 5, v6: 6, v7: 7, v8: 8 }[t.version];
    if (o === void 0) throw Error(`Invalid UUID version: "${t.version}"`);
    t.pattern ?? (t.pattern = Ro(o));
  } else t.pattern ?? (t.pattern = Ro());
  xe.init(e, t);
});
var cp = b("$ZodEmail", (e, t) => {
  t.pattern ?? (t.pattern = hb), xe.init(e, t);
});
var lp = b("$ZodURL", (e, t) => {
  xe.init(e, t), e._zod.check = (r) => {
    try {
      let o = r.value, n = new URL(o), i = n.href;
      if (t.hostname) {
        if (t.hostname.lastIndex = 0, !t.hostname.test(n.hostname)) r.issues.push({ code: "invalid_format", format: "url", note: "Invalid hostname", pattern: wb.source, input: r.value, inst: e, continue: !t.abort });
      }
      if (t.protocol) {
        if (t.protocol.lastIndex = 0, !t.protocol.test(n.protocol.endsWith(":") ? n.protocol.slice(0, -1) : n.protocol)) r.issues.push({ code: "invalid_format", format: "url", note: "Invalid protocol", pattern: t.protocol.source, input: r.value, inst: e, continue: !t.abort });
      }
      if (!o.endsWith("/") && i.endsWith("/")) r.value = i.slice(0, -1);
      else r.value = i;
      return;
    } catch (o) {
      r.issues.push({ code: "invalid_format", format: "url", input: r.value, inst: e, continue: !t.abort });
    }
  };
});
var up = b("$ZodEmoji", (e, t) => {
  t.pattern ?? (t.pattern = yb()), xe.init(e, t);
});
var dp = b("$ZodNanoID", (e, t) => {
  t.pattern ?? (t.pattern = fb), xe.init(e, t);
});
var pp = b("$ZodCUID", (e, t) => {
  t.pattern ?? (t.pattern = cb), xe.init(e, t);
});
var fp = b("$ZodCUID2", (e, t) => {
  t.pattern ?? (t.pattern = lb), xe.init(e, t);
});
var mp = b("$ZodULID", (e, t) => {
  t.pattern ?? (t.pattern = ub), xe.init(e, t);
});
var gp = b("$ZodXID", (e, t) => {
  t.pattern ?? (t.pattern = db), xe.init(e, t);
});
var hp = b("$ZodKSUID", (e, t) => {
  t.pattern ?? (t.pattern = pb), xe.init(e, t);
});
var n_ = b("$ZodISODateTime", (e, t) => {
  t.pattern ?? (t.pattern = Pb(t)), xe.init(e, t);
});
var o_ = b("$ZodISODate", (e, t) => {
  t.pattern ?? (t.pattern = Eb), xe.init(e, t);
});
var i_ = b("$ZodISOTime", (e, t) => {
  t.pattern ?? (t.pattern = Tb(t)), xe.init(e, t);
});
var s_ = b("$ZodISODuration", (e, t) => {
  t.pattern ?? (t.pattern = mb), xe.init(e, t);
});
var yp = b("$ZodIPv4", (e, t) => {
  t.pattern ?? (t.pattern = bb), xe.init(e, t), e._zod.onattach.push((r) => {
    let o = r._zod.bag;
    o.format = "ipv4";
  });
});
var bp = b("$ZodIPv6", (e, t) => {
  t.pattern ?? (t.pattern = _b), xe.init(e, t), e._zod.onattach.push((r) => {
    let o = r._zod.bag;
    o.format = "ipv6";
  }), e._zod.check = (r) => {
    try {
      new URL(`http://[${r.value}]`);
    } catch {
      r.issues.push({ code: "invalid_format", format: "ipv6", input: r.value, inst: e, continue: !t.abort });
    }
  };
});
var _p = b("$ZodCIDRv4", (e, t) => {
  t.pattern ?? (t.pattern = vb), xe.init(e, t);
});
var vp = b("$ZodCIDRv6", (e, t) => {
  t.pattern ?? (t.pattern = Sb), xe.init(e, t), e._zod.check = (r) => {
    let [o, n] = r.value.split("/");
    try {
      if (!n) throw Error();
      let i = Number(n);
      if (`${i}` !== n) throw Error();
      if (i < 0 || i > 128) throw Error();
      new URL(`http://[${o}]`);
    } catch {
      r.issues.push({ code: "invalid_format", format: "cidrv6", input: r.value, inst: e, continue: !t.abort });
    }
  };
});
function a_(e) {
  if (e === "") return true;
  if (e.length % 4 !== 0) return false;
  try {
    return atob(e), true;
  } catch {
    return false;
  }
}
var Sp = b("$ZodBase64", (e, t) => {
  t.pattern ?? (t.pattern = xb), xe.init(e, t), e._zod.onattach.push((r) => {
    r._zod.bag.contentEncoding = "base64";
  }), e._zod.check = (r) => {
    if (a_(r.value)) return;
    r.issues.push({ code: "invalid_format", format: "base64", input: r.value, inst: e, continue: !t.abort });
  };
});
function W0(e) {
  if (!ep.test(e)) return false;
  let t = e.replace(/[-_]/g, (o) => o === "-" ? "+" : "/"), r = t.padEnd(Math.ceil(t.length / 4) * 4, "=");
  return a_(r);
}
var xp = b("$ZodBase64URL", (e, t) => {
  t.pattern ?? (t.pattern = ep), xe.init(e, t), e._zod.onattach.push((r) => {
    r._zod.bag.contentEncoding = "base64url";
  }), e._zod.check = (r) => {
    if (W0(r.value)) return;
    r.issues.push({ code: "invalid_format", format: "base64url", input: r.value, inst: e, continue: !t.abort });
  };
});
var wp = b("$ZodE164", (e, t) => {
  t.pattern ?? (t.pattern = kb), xe.init(e, t);
});
function K0(e, t = null) {
  try {
    let r = e.split(".");
    if (r.length !== 3) return false;
    let [o] = r;
    if (!o) return false;
    let n = JSON.parse(atob(o));
    if ("typ" in n && n?.typ !== "JWT") return false;
    if (!n.alg) return false;
    if (t && (!("alg" in n) || n.alg !== t)) return false;
    return true;
  } catch {
    return false;
  }
}
var kp = b("$ZodJWT", (e, t) => {
  xe.init(e, t), e._zod.check = (r) => {
    if (K0(r.value, t.alg)) return;
    r.issues.push({ code: "invalid_format", format: "jwt", input: r.value, inst: e, continue: !t.abort });
  };
});
var Ep = b("$ZodCustomStringFormat", (e, t) => {
  xe.init(e, t), e._zod.check = (r) => {
    if (t.fn(r.value)) return;
    r.issues.push({ code: "invalid_format", format: t.format, input: r.value, inst: e, continue: !t.abort });
  };
});
var Ec = b("$ZodNumber", (e, t) => {
  G.init(e, t), e._zod.pattern = e._zod.bag.pattern ?? Ob, e._zod.parse = (r, o) => {
    if (t.coerce) try {
      r.value = Number(r.value);
    } catch (s) {
    }
    let n = r.value;
    if (typeof n === "number" && !Number.isNaN(n) && Number.isFinite(n)) return r;
    let i = typeof n === "number" ? Number.isNaN(n) ? "NaN" : !Number.isFinite(n) ? "Infinity" : void 0 : void 0;
    return r.issues.push({ expected: "number", code: "invalid_type", input: n, inst: e, ...i ? { received: i } : {} }), r;
  };
});
var Tp = b("$ZodNumber", (e, t) => {
  Ub.init(e, t), Ec.init(e, t);
});
var Ji = b("$ZodBoolean", (e, t) => {
  G.init(e, t), e._zod.pattern = Ab, e._zod.parse = (r, o) => {
    if (t.coerce) try {
      r.value = Boolean(r.value);
    } catch (i) {
    }
    let n = r.value;
    if (typeof n === "boolean") return r;
    return r.issues.push({ expected: "boolean", code: "invalid_type", input: n, inst: e }), r;
  };
});
var Tc = b("$ZodBigInt", (e, t) => {
  G.init(e, t), e._zod.pattern = Rb, e._zod.parse = (r, o) => {
    if (t.coerce) try {
      r.value = BigInt(r.value);
    } catch (n) {
    }
    if (typeof r.value === "bigint") return r;
    return r.issues.push({ expected: "bigint", code: "invalid_type", input: r.value, inst: e }), r;
  };
});
var Pp = b("$ZodBigInt", (e, t) => {
  zb.init(e, t), Tc.init(e, t);
});
var Ip = b("$ZodSymbol", (e, t) => {
  G.init(e, t), e._zod.parse = (r, o) => {
    let n = r.value;
    if (typeof n === "symbol") return r;
    return r.issues.push({ expected: "symbol", code: "invalid_type", input: n, inst: e }), r;
  };
});
var Rp = b("$ZodUndefined", (e, t) => {
  G.init(e, t), e._zod.pattern = Mb, e._zod.values = /* @__PURE__ */ new Set([void 0]), e._zod.optin = "optional", e._zod.optout = "optional", e._zod.parse = (r, o) => {
    let n = r.value;
    if (typeof n > "u") return r;
    return r.issues.push({ expected: "undefined", code: "invalid_type", input: n, inst: e }), r;
  };
});
var $p = b("$ZodNull", (e, t) => {
  G.init(e, t), e._zod.pattern = Cb, e._zod.values = /* @__PURE__ */ new Set([null]), e._zod.parse = (r, o) => {
    let n = r.value;
    if (n === null) return r;
    return r.issues.push({ expected: "null", code: "invalid_type", input: n, inst: e }), r;
  };
});
var Op = b("$ZodAny", (e, t) => {
  G.init(e, t), e._zod.parse = (r) => r;
});
var $o = b("$ZodUnknown", (e, t) => {
  G.init(e, t), e._zod.parse = (r) => r;
});
var Ap = b("$ZodNever", (e, t) => {
  G.init(e, t), e._zod.parse = (r, o) => (r.issues.push({ expected: "never", code: "invalid_type", input: r.value, inst: e }), r);
});
var Cp = b("$ZodVoid", (e, t) => {
  G.init(e, t), e._zod.parse = (r, o) => {
    let n = r.value;
    if (typeof n > "u") return r;
    return r.issues.push({ expected: "void", code: "invalid_type", input: n, inst: e }), r;
  };
});
var Mp = b("$ZodDate", (e, t) => {
  G.init(e, t), e._zod.parse = (r, o) => {
    if (t.coerce) try {
      r.value = new Date(r.value);
    } catch (a) {
    }
    let n = r.value, i = n instanceof Date;
    if (i && !Number.isNaN(n.getTime())) return r;
    return r.issues.push({ expected: "date", code: "invalid_type", input: n, ...i ? { received: "Invalid Date" } : {}, inst: e }), r;
  };
});
function N0(e, t, r) {
  if (e.issues.length) t.issues.push(...wt(r, e.issues));
  t.value[r] = e.value;
}
var Xi = b("$ZodArray", (e, t) => {
  G.init(e, t), e._zod.parse = (r, o) => {
    let n = r.value;
    if (!Array.isArray(n)) return r.issues.push({ expected: "array", code: "invalid_type", input: n, inst: e }), r;
    r.value = Array(n.length);
    let i = [];
    for (let s = 0; s < n.length; s++) {
      let a = n[s], c = t.element._zod.run({ value: a, issues: [] }, o);
      if (c instanceof Promise) i.push(c.then((u) => N0(u, r, s)));
      else N0(c, r, s);
    }
    if (i.length) return Promise.all(i).then(() => r);
    return r;
  };
});
function op(e, t, r) {
  if (e.issues.length) t.issues.push(...wt(r, e.issues));
  t.value[r] = e.value;
}
function j0(e, t, r, o) {
  if (e.issues.length) if (o[r] === void 0) if (r in o) t.value[r] = void 0;
  else t.value[r] = e.value;
  else t.issues.push(...wt(r, e.issues));
  else if (e.value === void 0) {
    if (r in o) t.value[r] = void 0;
  } else t.value[r] = e.value;
}
var Pc = b("$ZodObject", (e, t) => {
  G.init(e, t);
  let r = _c(() => {
    let p = Object.keys(t.shape);
    for (let m of p) if (!(t.shape[m] instanceof G)) throw Error(`Invalid element at key "${m}": expected a Zod schema`);
    let f = ob(t.shape);
    return { shape: t.shape, keys: p, keySet: new Set(p), numKeys: p.length, optionalKeys: new Set(f) };
  });
  me(e._zod, "propValues", () => {
    let p = t.shape, f = {};
    for (let m in p) {
      let g = p[m]._zod;
      if (g.values) {
        f[m] ?? (f[m] = /* @__PURE__ */ new Set());
        for (let h of g.values) f[m].add(h);
      }
    }
    return f;
  });
  let o = (p) => {
    let f = new np(["shape", "payload", "ctx"]), m = r.value, g = (x) => {
      let w = Eo(x);
      return `shape[${w}]._zod.run({ value: input[${w}], issues: [] }, ctx)`;
    };
    f.write("const input = payload.value;");
    let h = /* @__PURE__ */ Object.create(null), y = 0;
    for (let x of m.keys) h[x] = `key_${y++}`;
    f.write("const newResult = {}");
    for (let x of m.keys) if (m.optionalKeys.has(x)) {
      let w = h[x];
      f.write(`const ${w} = ${g(x)};`);
      let A = Eo(x);
      f.write(`
        if (${w}.issues.length) {
          if (input[${A}] === undefined) {
            if (${A} in input) {
              newResult[${A}] = undefined;
            }
          } else {
            payload.issues = payload.issues.concat(
              ${w}.issues.map((iss) => ({
                ...iss,
                path: iss.path ? [${A}, ...iss.path] : [${A}],
              }))
            );
          }
        } else if (${w}.value === undefined) {
          if (${A} in input) newResult[${A}] = undefined;
        } else {
          newResult[${A}] = ${w}.value;
        }
        `);
    } else {
      let w = h[x];
      f.write(`const ${w} = ${g(x)};`), f.write(`
          if (${w}.issues.length) payload.issues = payload.issues.concat(${w}.issues.map(iss => ({
            ...iss,
            path: iss.path ? [${Eo(x)}, ...iss.path] : [${Eo(x)}]
          })));`), f.write(`newResult[${Eo(x)}] = ${w}.value`);
    }
    f.write("payload.value = newResult;"), f.write("return payload;");
    let v = f.compile();
    return (x, w) => v(p, x, w);
  }, n, i = qi, s = !hc.jitless, c = s && rb.value, u = t.catchall, d;
  e._zod.parse = (p, f) => {
    d ?? (d = r.value);
    let m = p.value;
    if (!i(m)) return p.issues.push({ expected: "object", code: "invalid_type", input: m, inst: e }), p;
    let g = [];
    if (s && c && f?.async === false && f.jitless !== true) {
      if (!n) n = o(t.shape);
      p = n(p, f);
    } else {
      p.value = {};
      let w = d.shape;
      for (let A of d.keys) {
        let U = w[A], se = U._zod.run({ value: m[A], issues: [] }, f), Le = U._zod.optin === "optional" && U._zod.optout === "optional";
        if (se instanceof Promise) g.push(se.then((Ye) => Le ? j0(Ye, p, A, m) : op(Ye, p, A)));
        else if (Le) j0(se, p, A, m);
        else op(se, p, A);
      }
    }
    if (!u) return g.length ? Promise.all(g).then(() => p) : p;
    let h = [], y = d.keySet, v = u._zod, x = v.def.type;
    for (let w of Object.keys(m)) {
      if (y.has(w)) continue;
      if (x === "never") {
        h.push(w);
        continue;
      }
      let A = v.run({ value: m[w], issues: [] }, f);
      if (A instanceof Promise) g.push(A.then((U) => op(U, p, w)));
      else op(A, p, w);
    }
    if (h.length) p.issues.push({ code: "unrecognized_keys", keys: h, input: m, inst: e });
    if (!g.length) return p;
    return Promise.all(g).then(() => p);
  };
});
function U0(e, t, r, o) {
  for (let n of e) if (n.issues.length === 0) return t.value = n.value, t;
  return t.issues.push({ code: "invalid_union", input: t.value, inst: r, errors: e.map((n) => n.issues.map((i) => jt(i, o, He()))) }), t;
}
var Ic = b("$ZodUnion", (e, t) => {
  G.init(e, t), me(e._zod, "optin", () => t.options.some((r) => r._zod.optin === "optional") ? "optional" : void 0), me(e._zod, "optout", () => t.options.some((r) => r._zod.optout === "optional") ? "optional" : void 0), me(e._zod, "values", () => {
    if (t.options.every((r) => r._zod.values)) return new Set(t.options.flatMap((r) => Array.from(r._zod.values)));
    return;
  }), me(e._zod, "pattern", () => {
    if (t.options.every((r) => r._zod.pattern)) {
      let r = t.options.map((o) => o._zod.pattern);
      return new RegExp(`^(${r.map((o) => vc(o.source)).join("|")})$`);
    }
    return;
  }), e._zod.parse = (r, o) => {
    let n = false, i = [];
    for (let s of t.options) {
      let a = s._zod.run({ value: r.value, issues: [] }, o);
      if (a instanceof Promise) i.push(a), n = true;
      else {
        if (a.issues.length === 0) return a;
        i.push(a);
      }
    }
    if (!n) return U0(i, r, e, o);
    return Promise.all(i).then((s) => U0(s, r, e, o));
  };
});
var Dp = b("$ZodDiscriminatedUnion", (e, t) => {
  Ic.init(e, t);
  let r = e._zod.parse;
  me(e._zod, "propValues", () => {
    let n = {};
    for (let i of t.options) {
      let s = i._zod.propValues;
      if (!s || Object.keys(s).length === 0) throw Error(`Invalid discriminated union option at index "${t.options.indexOf(i)}"`);
      for (let [a, c] of Object.entries(s)) {
        if (!n[a]) n[a] = /* @__PURE__ */ new Set();
        for (let u of c) n[a].add(u);
      }
    }
    return n;
  });
  let o = _c(() => {
    let n = t.options, i = /* @__PURE__ */ new Map();
    for (let s of n) {
      let a = s._zod.propValues[t.discriminator];
      if (!a || a.size === 0) throw Error(`Invalid discriminated union option at index "${t.options.indexOf(s)}"`);
      for (let c of a) {
        if (i.has(c)) throw Error(`Duplicate discriminator value "${String(c)}"`);
        i.set(c, s);
      }
    }
    return i;
  });
  e._zod.parse = (n, i) => {
    let s = n.value;
    if (!qi(s)) return n.issues.push({ code: "invalid_type", expected: "object", input: s, inst: e }), n;
    let a = o.value.get(s?.[t.discriminator]);
    if (a) return a._zod.run(n, i);
    if (t.unionFallback) return r(n, i);
    return n.issues.push({ code: "invalid_union", errors: [], note: "No matching discriminator", input: s, path: [t.discriminator], inst: e }), n;
  };
});
var Np = b("$ZodIntersection", (e, t) => {
  G.init(e, t), e._zod.parse = (r, o) => {
    let n = r.value, i = t.left._zod.run({ value: n, issues: [] }, o), s = t.right._zod.run({ value: n, issues: [] }, o);
    if (i instanceof Promise || s instanceof Promise) return Promise.all([i, s]).then(([c, u]) => z0(r, c, u));
    return z0(r, i, s);
  };
});
function r_(e, t) {
  if (e === t) return { valid: true, data: e };
  if (e instanceof Date && t instanceof Date && +e === +t) return { valid: true, data: e };
  if (Zi(e) && Zi(t)) {
    let r = Object.keys(t), o = Object.keys(e).filter((i) => r.indexOf(i) !== -1), n = { ...e, ...t };
    for (let i of o) {
      let s = r_(e[i], t[i]);
      if (!s.valid) return { valid: false, mergeErrorPath: [i, ...s.mergeErrorPath] };
      n[i] = s.data;
    }
    return { valid: true, data: n };
  }
  if (Array.isArray(e) && Array.isArray(t)) {
    if (e.length !== t.length) return { valid: false, mergeErrorPath: [] };
    let r = [];
    for (let o = 0; o < e.length; o++) {
      let n = e[o], i = t[o], s = r_(n, i);
      if (!s.valid) return { valid: false, mergeErrorPath: [o, ...s.mergeErrorPath] };
      r.push(s.data);
    }
    return { valid: true, data: r };
  }
  return { valid: false, mergeErrorPath: [] };
}
function z0(e, t, r) {
  if (t.issues.length) e.issues.push(...t.issues);
  if (r.issues.length) e.issues.push(...r.issues);
  if (To(e)) return e;
  let o = r_(t.value, r.value);
  if (!o.valid) throw Error(`Unmergable intersection. Error path: ${JSON.stringify(o.mergeErrorPath)}`);
  return e.value = o.data, e;
}
var An = b("$ZodTuple", (e, t) => {
  G.init(e, t);
  let r = t.items, o = r.length - [...r].reverse().findIndex((n) => n._zod.optin !== "optional");
  e._zod.parse = (n, i) => {
    let s = n.value;
    if (!Array.isArray(s)) return n.issues.push({ input: s, inst: e, expected: "tuple", code: "invalid_type" }), n;
    n.value = [];
    let a = [];
    if (!t.rest) {
      let u = s.length > r.length, d = s.length < o - 1;
      if (u || d) return n.issues.push({ input: s, inst: e, origin: "array", ...u ? { code: "too_big", maximum: r.length } : { code: "too_small", minimum: r.length } }), n;
    }
    let c = -1;
    for (let u of r) {
      if (c++, c >= s.length) {
        if (c >= o) continue;
      }
      let d = u._zod.run({ value: s[c], issues: [] }, i);
      if (d instanceof Promise) a.push(d.then((p) => ip(p, n, c)));
      else ip(d, n, c);
    }
    if (t.rest) {
      let u = s.slice(r.length);
      for (let d of u) {
        c++;
        let p = t.rest._zod.run({ value: d, issues: [] }, i);
        if (p instanceof Promise) a.push(p.then((f) => ip(f, n, c)));
        else ip(p, n, c);
      }
    }
    if (a.length) return Promise.all(a).then(() => n);
    return n;
  };
});
function ip(e, t, r) {
  if (e.issues.length) t.issues.push(...wt(r, e.issues));
  t.value[r] = e.value;
}
var jp = b("$ZodRecord", (e, t) => {
  G.init(e, t), e._zod.parse = (r, o) => {
    let n = r.value;
    if (!Zi(n)) return r.issues.push({ expected: "record", code: "invalid_type", input: n, inst: e }), r;
    let i = [];
    if (t.keyType._zod.values) {
      let s = t.keyType._zod.values;
      r.value = {};
      for (let c of s) if (typeof c === "string" || typeof c === "number" || typeof c === "symbol") {
        let u = t.valueType._zod.run({ value: n[c], issues: [] }, o);
        if (u instanceof Promise) i.push(u.then((d) => {
          if (d.issues.length) r.issues.push(...wt(c, d.issues));
          r.value[c] = d.value;
        }));
        else {
          if (u.issues.length) r.issues.push(...wt(c, u.issues));
          r.value[c] = u.value;
        }
      }
      let a;
      for (let c in n) if (!s.has(c)) a = a ?? [], a.push(c);
      if (a && a.length > 0) r.issues.push({ code: "unrecognized_keys", input: n, inst: e, keys: a });
    } else {
      r.value = {};
      for (let s of Reflect.ownKeys(n)) {
        if (s === "__proto__") continue;
        let a = t.keyType._zod.run({ value: s, issues: [] }, o);
        if (a instanceof Promise) throw Error("Async schemas not supported in object keys currently");
        if (a.issues.length) {
          r.issues.push({ origin: "record", code: "invalid_key", issues: a.issues.map((u) => jt(u, o, He())), input: s, path: [s], inst: e }), r.value[a.value] = a.value;
          continue;
        }
        let c = t.valueType._zod.run({ value: n[s], issues: [] }, o);
        if (c instanceof Promise) i.push(c.then((u) => {
          if (u.issues.length) r.issues.push(...wt(s, u.issues));
          r.value[a.value] = u.value;
        }));
        else {
          if (c.issues.length) r.issues.push(...wt(s, c.issues));
          r.value[a.value] = c.value;
        }
      }
    }
    if (i.length) return Promise.all(i).then(() => r);
    return r;
  };
});
var Up = b("$ZodMap", (e, t) => {
  G.init(e, t), e._zod.parse = (r, o) => {
    let n = r.value;
    if (!(n instanceof Map)) return r.issues.push({ expected: "map", code: "invalid_type", input: n, inst: e }), r;
    let i = [];
    r.value = /* @__PURE__ */ new Map();
    for (let [s, a] of n) {
      let c = t.keyType._zod.run({ value: s, issues: [] }, o), u = t.valueType._zod.run({ value: a, issues: [] }, o);
      if (c instanceof Promise || u instanceof Promise) i.push(Promise.all([c, u]).then(([d, p]) => {
        L0(d, p, r, s, n, e, o);
      }));
      else L0(c, u, r, s, n, e, o);
    }
    if (i.length) return Promise.all(i).then(() => r);
    return r;
  };
});
function L0(e, t, r, o, n, i, s) {
  if (e.issues.length) if (Sc.has(typeof o)) r.issues.push(...wt(o, e.issues));
  else r.issues.push({ origin: "map", code: "invalid_key", input: n, inst: i, issues: e.issues.map((a) => jt(a, s, He())) });
  if (t.issues.length) if (Sc.has(typeof o)) r.issues.push(...wt(o, t.issues));
  else r.issues.push({ origin: "map", code: "invalid_element", input: n, inst: i, key: o, issues: t.issues.map((a) => jt(a, s, He())) });
  r.value.set(e.value, t.value);
}
var zp = b("$ZodSet", (e, t) => {
  G.init(e, t), e._zod.parse = (r, o) => {
    let n = r.value;
    if (!(n instanceof Set)) return r.issues.push({ input: n, inst: e, expected: "set", code: "invalid_type" }), r;
    let i = [];
    r.value = /* @__PURE__ */ new Set();
    for (let s of n) {
      let a = t.valueType._zod.run({ value: s, issues: [] }, o);
      if (a instanceof Promise) i.push(a.then((c) => F0(c, r)));
      else F0(a, r);
    }
    if (i.length) return Promise.all(i).then(() => r);
    return r;
  };
});
function F0(e, t) {
  if (e.issues.length) t.issues.push(...e.issues);
  t.value.add(e.value);
}
var Lp = b("$ZodEnum", (e, t) => {
  G.init(e, t);
  let r = bc(t.entries);
  e._zod.values = new Set(r), e._zod.pattern = new RegExp(`^(${r.filter((o) => Sc.has(typeof o)).map((o) => typeof o === "string" ? Gr(o) : o.toString()).join("|")})$`), e._zod.parse = (o, n) => {
    let i = o.value;
    if (e._zod.values.has(i)) return o;
    return o.issues.push({ code: "invalid_value", values: r, input: i, inst: e }), o;
  };
});
var Fp = b("$ZodLiteral", (e, t) => {
  G.init(e, t), e._zod.values = new Set(t.values), e._zod.pattern = new RegExp(`^(${t.values.map((r) => typeof r === "string" ? Gr(r) : r ? r.toString() : String(r)).join("|")})$`), e._zod.parse = (r, o) => {
    let n = r.value;
    if (e._zod.values.has(n)) return r;
    return r.issues.push({ code: "invalid_value", values: t.values, input: n, inst: e }), r;
  };
});
var Bp = b("$ZodFile", (e, t) => {
  G.init(e, t), e._zod.parse = (r, o) => {
    let n = r.value;
    if (n instanceof File) return r;
    return r.issues.push({ expected: "file", code: "invalid_type", input: n, inst: e }), r;
  };
});
var Yi = b("$ZodTransform", (e, t) => {
  G.init(e, t), e._zod.parse = (r, o) => {
    let n = t.transform(r.value, r);
    if (o.async) return (n instanceof Promise ? n : Promise.resolve(n)).then((s) => (r.value = s, r));
    if (n instanceof Promise) throw new Kr();
    return r.value = n, r;
  };
});
var Hp = b("$ZodOptional", (e, t) => {
  G.init(e, t), e._zod.optin = "optional", e._zod.optout = "optional", me(e._zod, "values", () => t.innerType._zod.values ? /* @__PURE__ */ new Set([...t.innerType._zod.values, void 0]) : void 0), me(e._zod, "pattern", () => {
    let r = t.innerType._zod.pattern;
    return r ? new RegExp(`^(${vc(r.source)})?$`) : void 0;
  }), e._zod.parse = (r, o) => {
    if (t.innerType._zod.optin === "optional") return t.innerType._zod.run(r, o);
    if (r.value === void 0) return r;
    return t.innerType._zod.run(r, o);
  };
});
var qp = b("$ZodNullable", (e, t) => {
  G.init(e, t), me(e._zod, "optin", () => t.innerType._zod.optin), me(e._zod, "optout", () => t.innerType._zod.optout), me(e._zod, "pattern", () => {
    let r = t.innerType._zod.pattern;
    return r ? new RegExp(`^(${vc(r.source)}|null)$`) : void 0;
  }), me(e._zod, "values", () => t.innerType._zod.values ? /* @__PURE__ */ new Set([...t.innerType._zod.values, null]) : void 0), e._zod.parse = (r, o) => {
    if (r.value === null) return r;
    return t.innerType._zod.run(r, o);
  };
});
var Zp = b("$ZodDefault", (e, t) => {
  G.init(e, t), e._zod.optin = "optional", me(e._zod, "values", () => t.innerType._zod.values), e._zod.parse = (r, o) => {
    if (r.value === void 0) return r.value = t.defaultValue, r;
    let n = t.innerType._zod.run(r, o);
    if (n instanceof Promise) return n.then((i) => B0(i, t));
    return B0(n, t);
  };
});
function B0(e, t) {
  if (e.value === void 0) e.value = t.defaultValue;
  return e;
}
var Vp = b("$ZodPrefault", (e, t) => {
  G.init(e, t), e._zod.optin = "optional", me(e._zod, "values", () => t.innerType._zod.values), e._zod.parse = (r, o) => {
    if (r.value === void 0) r.value = t.defaultValue;
    return t.innerType._zod.run(r, o);
  };
});
var Wp = b("$ZodNonOptional", (e, t) => {
  G.init(e, t), me(e._zod, "values", () => {
    let r = t.innerType._zod.values;
    return r ? new Set([...r].filter((o) => o !== void 0)) : void 0;
  }), e._zod.parse = (r, o) => {
    let n = t.innerType._zod.run(r, o);
    if (n instanceof Promise) return n.then((i) => H0(i, e));
    return H0(n, e);
  };
});
function H0(e, t) {
  if (!e.issues.length && e.value === void 0) e.issues.push({ code: "invalid_type", expected: "nonoptional", input: e.value, inst: t });
  return e;
}
var Kp = b("$ZodSuccess", (e, t) => {
  G.init(e, t), e._zod.parse = (r, o) => {
    let n = t.innerType._zod.run(r, o);
    if (n instanceof Promise) return n.then((i) => (r.value = i.issues.length === 0, r));
    return r.value = n.issues.length === 0, r;
  };
});
var Gp = b("$ZodCatch", (e, t) => {
  G.init(e, t), e._zod.optin = "optional", me(e._zod, "optout", () => t.innerType._zod.optout), me(e._zod, "values", () => t.innerType._zod.values), e._zod.parse = (r, o) => {
    let n = t.innerType._zod.run(r, o);
    if (n instanceof Promise) return n.then((i) => {
      if (r.value = i.value, i.issues.length) r.value = t.catchValue({ ...r, error: { issues: i.issues.map((s) => jt(s, o, He())) }, input: r.value }), r.issues = [];
      return r;
    });
    if (r.value = n.value, n.issues.length) r.value = t.catchValue({ ...r, error: { issues: n.issues.map((i) => jt(i, o, He())) }, input: r.value }), r.issues = [];
    return r;
  };
});
var Jp = b("$ZodNaN", (e, t) => {
  G.init(e, t), e._zod.parse = (r, o) => {
    if (typeof r.value !== "number" || !Number.isNaN(r.value)) return r.issues.push({ input: r.value, inst: e, expected: "nan", code: "invalid_type" }), r;
    return r;
  };
});
var Qi = b("$ZodPipe", (e, t) => {
  G.init(e, t), me(e._zod, "values", () => t.in._zod.values), me(e._zod, "optin", () => t.in._zod.optin), me(e._zod, "optout", () => t.out._zod.optout), e._zod.parse = (r, o) => {
    let n = t.in._zod.run(r, o);
    if (n instanceof Promise) return n.then((i) => q0(i, t, o));
    return q0(n, t, o);
  };
});
function q0(e, t, r) {
  if (To(e)) return e;
  return t.out._zod.run({ value: e.value, issues: e.issues }, r);
}
var Xp = b("$ZodReadonly", (e, t) => {
  G.init(e, t), me(e._zod, "propValues", () => t.innerType._zod.propValues), me(e._zod, "values", () => t.innerType._zod.values), me(e._zod, "optin", () => t.innerType._zod.optin), me(e._zod, "optout", () => t.innerType._zod.optout), e._zod.parse = (r, o) => {
    let n = t.innerType._zod.run(r, o);
    if (n instanceof Promise) return n.then(Z0);
    return Z0(n);
  };
});
function Z0(e) {
  return e.value = Object.freeze(e.value), e;
}
var Yp = b("$ZodTemplateLiteral", (e, t) => {
  G.init(e, t);
  let r = [];
  for (let o of t.parts) if (o instanceof G) {
    if (!o._zod.pattern) throw Error(`Invalid template literal part, no pattern found: ${[...o._zod.traits].shift()}`);
    let n = o._zod.pattern instanceof RegExp ? o._zod.pattern.source : o._zod.pattern;
    if (!n) throw Error(`Invalid template literal part: ${o._zod.traits}`);
    let i = n.startsWith("^") ? 1 : 0, s = n.endsWith("$") ? n.length - 1 : n.length;
    r.push(n.slice(i, s));
  } else if (o === null || nb.has(typeof o)) r.push(Gr(`${o}`));
  else throw Error(`Invalid template literal part: ${o}`);
  e._zod.pattern = new RegExp(`^${r.join("")}$`), e._zod.parse = (o, n) => {
    if (typeof o.value !== "string") return o.issues.push({ input: o.value, inst: e, expected: "template_literal", code: "invalid_type" }), o;
    if (e._zod.pattern.lastIndex = 0, !e._zod.pattern.test(o.value)) return o.issues.push({ input: o.value, inst: e, code: "invalid_format", format: "template_literal", pattern: e._zod.pattern.source }), o;
    return o;
  };
});
var Qp = b("$ZodPromise", (e, t) => {
  G.init(e, t), e._zod.parse = (r, o) => Promise.resolve(r.value).then((n) => t.innerType._zod.run({ value: n, issues: [] }, o));
});
var ef = b("$ZodLazy", (e, t) => {
  G.init(e, t), me(e._zod, "innerType", () => t.getter()), me(e._zod, "pattern", () => e._zod.innerType._zod.pattern), me(e._zod, "propValues", () => e._zod.innerType._zod.propValues), me(e._zod, "optin", () => e._zod.innerType._zod.optin), me(e._zod, "optout", () => e._zod.innerType._zod.optout), e._zod.parse = (r, o) => e._zod.innerType._zod.run(r, o);
});
var tf = b("$ZodCustom", (e, t) => {
  Me.init(e, t), G.init(e, t), e._zod.parse = (r, o) => r, e._zod.check = (r) => {
    let o = r.value, n = t.fn(o);
    if (n instanceof Promise) return n.then((i) => V0(i, r, o, e));
    V0(n, r, o, e);
    return;
  };
});
function V0(e, t, r, o) {
  if (!e) {
    let n = { code: "custom", input: r, inst: o, path: [...o._zod.def.path ?? []], continue: !o._zod.def.abort };
    if (o._zod.def.params) n.params = o._zod.def.params;
    t.issues.push(ab(n));
  }
}
var es = {};
xr(es, { zhTW: () => Z_, zhCN: () => q_, vi: () => H_, ur: () => B_, ua: () => F_, tr: () => L_, th: () => z_, ta: () => U_, sv: () => j_, sl: () => N_, ru: () => D_, pt: () => M_, ps: () => A_, pl: () => C_, ota: () => O_, no: () => $_, nl: () => R_, ms: () => I_, mk: () => P_, ko: () => T_, kh: () => E_, ja: () => k_, it: () => w_, id: () => x_, hu: () => S_, he: () => v_, frCA: () => __, fr: () => b_, fi: () => y_, fa: () => h_, es: () => g_, eo: () => m_, en: () => Rc, de: () => f_, cs: () => p_, ca: () => d_, be: () => u_, az: () => l_, ar: () => c_ });
var nV = () => {
  let e = { string: { unit: "\u062D\u0631\u0641", verb: "\u0623\u0646 \u064A\u062D\u0648\u064A" }, file: { unit: "\u0628\u0627\u064A\u062A", verb: "\u0623\u0646 \u064A\u062D\u0648\u064A" }, array: { unit: "\u0639\u0646\u0635\u0631", verb: "\u0623\u0646 \u064A\u062D\u0648\u064A" }, set: { unit: "\u0639\u0646\u0635\u0631", verb: "\u0623\u0646 \u064A\u062D\u0648\u064A" } };
  function t(n) {
    return e[n] ?? null;
  }
  let r = (n) => {
    let i = typeof n;
    switch (i) {
      case "number":
        return Number.isNaN(n) ? "NaN" : "number";
      case "object": {
        if (Array.isArray(n)) return "array";
        if (n === null) return "null";
        if (Object.getPrototypeOf(n) !== Object.prototype && n.constructor) return n.constructor.name;
      }
    }
    return i;
  }, o = { regex: "\u0645\u062F\u062E\u0644", email: "\u0628\u0631\u064A\u062F \u0625\u0644\u0643\u062A\u0631\u0648\u0646\u064A", url: "\u0631\u0627\u0628\u0637", emoji: "\u0625\u064A\u0645\u0648\u062C\u064A", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "\u062A\u0627\u0631\u064A\u062E \u0648\u0648\u0642\u062A \u0628\u0645\u0639\u064A\u0627\u0631 ISO", date: "\u062A\u0627\u0631\u064A\u062E \u0628\u0645\u0639\u064A\u0627\u0631 ISO", time: "\u0648\u0642\u062A \u0628\u0645\u0639\u064A\u0627\u0631 ISO", duration: "\u0645\u062F\u0629 \u0628\u0645\u0639\u064A\u0627\u0631 ISO", ipv4: "\u0639\u0646\u0648\u0627\u0646 IPv4", ipv6: "\u0639\u0646\u0648\u0627\u0646 IPv6", cidrv4: "\u0645\u062F\u0649 \u0639\u0646\u0627\u0648\u064A\u0646 \u0628\u0635\u064A\u063A\u0629 IPv4", cidrv6: "\u0645\u062F\u0649 \u0639\u0646\u0627\u0648\u064A\u0646 \u0628\u0635\u064A\u063A\u0629 IPv6", base64: "\u0646\u064E\u0635 \u0628\u062A\u0631\u0645\u064A\u0632 base64-encoded", base64url: "\u0646\u064E\u0635 \u0628\u062A\u0631\u0645\u064A\u0632 base64url-encoded", json_string: "\u0646\u064E\u0635 \u0639\u0644\u0649 \u0647\u064A\u0626\u0629 JSON", e164: "\u0631\u0642\u0645 \u0647\u0627\u062A\u0641 \u0628\u0645\u0639\u064A\u0627\u0631 E.164", jwt: "JWT", template_literal: "\u0645\u062F\u062E\u0644" };
  return (n) => {
    switch (n.code) {
      case "invalid_type":
        return `\u0645\u062F\u062E\u0644\u0627\u062A \u063A\u064A\u0631 \u0645\u0642\u0628\u0648\u0644\u0629: \u064A\u0641\u062A\u0631\u0636 \u0625\u062F\u062E\u0627\u0644 ${n.expected}\u060C \u0648\u0644\u0643\u0646 \u062A\u0645 \u0625\u062F\u062E\u0627\u0644 ${r(n.input)}`;
      case "invalid_value":
        if (n.values.length === 1) return `\u0645\u062F\u062E\u0644\u0627\u062A \u063A\u064A\u0631 \u0645\u0642\u0628\u0648\u0644\u0629: \u064A\u0641\u062A\u0631\u0636 \u0625\u062F\u062E\u0627\u0644 ${D(n.values[0])}`;
        return `\u0627\u062E\u062A\u064A\u0627\u0631 \u063A\u064A\u0631 \u0645\u0642\u0628\u0648\u0644: \u064A\u062A\u0648\u0642\u0639 \u0627\u0646\u062A\u0642\u0627\u0621 \u0623\u062D\u062F \u0647\u0630\u0647 \u0627\u0644\u062E\u064A\u0627\u0631\u0627\u062A: ${T(n.values, "|")}`;
      case "too_big": {
        let i = n.inclusive ? "<=" : "<", s = t(n.origin);
        if (s) return ` \u0623\u0643\u0628\u0631 \u0645\u0646 \u0627\u0644\u0644\u0627\u0632\u0645: \u064A\u0641\u062A\u0631\u0636 \u0623\u0646 \u062A\u0643\u0648\u0646 ${n.origin ?? "\u0627\u0644\u0642\u064A\u0645\u0629"} ${i} ${n.maximum.toString()} ${s.unit ?? "\u0639\u0646\u0635\u0631"}`;
        return `\u0623\u0643\u0628\u0631 \u0645\u0646 \u0627\u0644\u0644\u0627\u0632\u0645: \u064A\u0641\u062A\u0631\u0636 \u0623\u0646 \u062A\u0643\u0648\u0646 ${n.origin ?? "\u0627\u0644\u0642\u064A\u0645\u0629"} ${i} ${n.maximum.toString()}`;
      }
      case "too_small": {
        let i = n.inclusive ? ">=" : ">", s = t(n.origin);
        if (s) return `\u0623\u0635\u063A\u0631 \u0645\u0646 \u0627\u0644\u0644\u0627\u0632\u0645: \u064A\u0641\u062A\u0631\u0636 \u0644\u0640 ${n.origin} \u0623\u0646 \u064A\u0643\u0648\u0646 ${i} ${n.minimum.toString()} ${s.unit}`;
        return `\u0623\u0635\u063A\u0631 \u0645\u0646 \u0627\u0644\u0644\u0627\u0632\u0645: \u064A\u0641\u062A\u0631\u0636 \u0644\u0640 ${n.origin} \u0623\u0646 \u064A\u0643\u0648\u0646 ${i} ${n.minimum.toString()}`;
      }
      case "invalid_format": {
        let i = n;
        if (i.format === "starts_with") return `\u0646\u064E\u0635 \u063A\u064A\u0631 \u0645\u0642\u0628\u0648\u0644: \u064A\u062C\u0628 \u0623\u0646 \u064A\u0628\u062F\u0623 \u0628\u0640 "${n.prefix}"`;
        if (i.format === "ends_with") return `\u0646\u064E\u0635 \u063A\u064A\u0631 \u0645\u0642\u0628\u0648\u0644: \u064A\u062C\u0628 \u0623\u0646 \u064A\u0646\u062A\u0647\u064A \u0628\u0640 "${i.suffix}"`;
        if (i.format === "includes") return `\u0646\u064E\u0635 \u063A\u064A\u0631 \u0645\u0642\u0628\u0648\u0644: \u064A\u062C\u0628 \u0623\u0646 \u064A\u062A\u0636\u0645\u0651\u064E\u0646 "${i.includes}"`;
        if (i.format === "regex") return `\u0646\u064E\u0635 \u063A\u064A\u0631 \u0645\u0642\u0628\u0648\u0644: \u064A\u062C\u0628 \u0623\u0646 \u064A\u0637\u0627\u0628\u0642 \u0627\u0644\u0646\u0645\u0637 ${i.pattern}`;
        return `${o[i.format] ?? n.format} \u063A\u064A\u0631 \u0645\u0642\u0628\u0648\u0644`;
      }
      case "not_multiple_of":
        return `\u0631\u0642\u0645 \u063A\u064A\u0631 \u0645\u0642\u0628\u0648\u0644: \u064A\u062C\u0628 \u0623\u0646 \u064A\u0643\u0648\u0646 \u0645\u0646 \u0645\u0636\u0627\u0639\u0641\u0627\u062A ${n.divisor}`;
      case "unrecognized_keys":
        return `\u0645\u0639\u0631\u0641${n.keys.length > 1 ? "\u0627\u062A" : ""} \u063A\u0631\u064A\u0628${n.keys.length > 1 ? "\u0629" : ""}: ${T(n.keys, "\u060C ")}`;
      case "invalid_key":
        return `\u0645\u0639\u0631\u0641 \u063A\u064A\u0631 \u0645\u0642\u0628\u0648\u0644 \u0641\u064A ${n.origin}`;
      case "invalid_union":
        return "\u0645\u062F\u062E\u0644 \u063A\u064A\u0631 \u0645\u0642\u0628\u0648\u0644";
      case "invalid_element":
        return `\u0645\u062F\u062E\u0644 \u063A\u064A\u0631 \u0645\u0642\u0628\u0648\u0644 \u0641\u064A ${n.origin}`;
      default:
        return "\u0645\u062F\u062E\u0644 \u063A\u064A\u0631 \u0645\u0642\u0628\u0648\u0644";
    }
  };
};
function c_() {
  return { localeError: nV() };
}
var oV = () => {
  let e = { string: { unit: "simvol", verb: "olmal\u0131d\u0131r" }, file: { unit: "bayt", verb: "olmal\u0131d\u0131r" }, array: { unit: "element", verb: "olmal\u0131d\u0131r" }, set: { unit: "element", verb: "olmal\u0131d\u0131r" } };
  function t(n) {
    return e[n] ?? null;
  }
  let r = (n) => {
    let i = typeof n;
    switch (i) {
      case "number":
        return Number.isNaN(n) ? "NaN" : "number";
      case "object": {
        if (Array.isArray(n)) return "array";
        if (n === null) return "null";
        if (Object.getPrototypeOf(n) !== Object.prototype && n.constructor) return n.constructor.name;
      }
    }
    return i;
  }, o = { regex: "input", email: "email address", url: "URL", emoji: "emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "ISO datetime", date: "ISO date", time: "ISO time", duration: "ISO duration", ipv4: "IPv4 address", ipv6: "IPv6 address", cidrv4: "IPv4 range", cidrv6: "IPv6 range", base64: "base64-encoded string", base64url: "base64url-encoded string", json_string: "JSON string", e164: "E.164 number", jwt: "JWT", template_literal: "input" };
  return (n) => {
    switch (n.code) {
      case "invalid_type":
        return `Yanl\u0131\u015F d\u0259y\u0259r: g\xF6zl\u0259nil\u0259n ${n.expected}, daxil olan ${r(n.input)}`;
      case "invalid_value":
        if (n.values.length === 1) return `Yanl\u0131\u015F d\u0259y\u0259r: g\xF6zl\u0259nil\u0259n ${D(n.values[0])}`;
        return `Yanl\u0131\u015F se\xE7im: a\u015Fa\u011F\u0131dak\u0131lardan biri olmal\u0131d\u0131r: ${T(n.values, "|")}`;
      case "too_big": {
        let i = n.inclusive ? "<=" : "<", s = t(n.origin);
        if (s) return `\xC7ox b\xF6y\xFCk: g\xF6zl\u0259nil\u0259n ${n.origin ?? "d\u0259y\u0259r"} ${i}${n.maximum.toString()} ${s.unit ?? "element"}`;
        return `\xC7ox b\xF6y\xFCk: g\xF6zl\u0259nil\u0259n ${n.origin ?? "d\u0259y\u0259r"} ${i}${n.maximum.toString()}`;
      }
      case "too_small": {
        let i = n.inclusive ? ">=" : ">", s = t(n.origin);
        if (s) return `\xC7ox ki\xE7ik: g\xF6zl\u0259nil\u0259n ${n.origin} ${i}${n.minimum.toString()} ${s.unit}`;
        return `\xC7ox ki\xE7ik: g\xF6zl\u0259nil\u0259n ${n.origin} ${i}${n.minimum.toString()}`;
      }
      case "invalid_format": {
        let i = n;
        if (i.format === "starts_with") return `Yanl\u0131\u015F m\u0259tn: "${i.prefix}" il\u0259 ba\u015Flamal\u0131d\u0131r`;
        if (i.format === "ends_with") return `Yanl\u0131\u015F m\u0259tn: "${i.suffix}" il\u0259 bitm\u0259lidir`;
        if (i.format === "includes") return `Yanl\u0131\u015F m\u0259tn: "${i.includes}" daxil olmal\u0131d\u0131r`;
        if (i.format === "regex") return `Yanl\u0131\u015F m\u0259tn: ${i.pattern} \u015Fablonuna uy\u011Fun olmal\u0131d\u0131r`;
        return `Yanl\u0131\u015F ${o[i.format] ?? n.format}`;
      }
      case "not_multiple_of":
        return `Yanl\u0131\u015F \u0259d\u0259d: ${n.divisor} il\u0259 b\xF6l\xFCn\u0259 bil\u0259n olmal\u0131d\u0131r`;
      case "unrecognized_keys":
        return `Tan\u0131nmayan a\xE7ar${n.keys.length > 1 ? "lar" : ""}: ${T(n.keys, ", ")}`;
      case "invalid_key":
        return `${n.origin} daxilind\u0259 yanl\u0131\u015F a\xE7ar`;
      case "invalid_union":
        return "Yanl\u0131\u015F d\u0259y\u0259r";
      case "invalid_element":
        return `${n.origin} daxilind\u0259 yanl\u0131\u015F d\u0259y\u0259r`;
      default:
        return "Yanl\u0131\u015F d\u0259y\u0259r";
    }
  };
};
function l_() {
  return { localeError: oV() };
}
function J0(e, t, r, o) {
  let n = Math.abs(e), i = n % 10, s = n % 100;
  if (s >= 11 && s <= 19) return o;
  if (i === 1) return t;
  if (i >= 2 && i <= 4) return r;
  return o;
}
var iV = () => {
  let e = { string: { unit: { one: "\u0441\u0456\u043C\u0432\u0430\u043B", few: "\u0441\u0456\u043C\u0432\u0430\u043B\u044B", many: "\u0441\u0456\u043C\u0432\u0430\u043B\u0430\u045E" }, verb: "\u043C\u0435\u0446\u044C" }, array: { unit: { one: "\u044D\u043B\u0435\u043C\u0435\u043D\u0442", few: "\u044D\u043B\u0435\u043C\u0435\u043D\u0442\u044B", many: "\u044D\u043B\u0435\u043C\u0435\u043D\u0442\u0430\u045E" }, verb: "\u043C\u0435\u0446\u044C" }, set: { unit: { one: "\u044D\u043B\u0435\u043C\u0435\u043D\u0442", few: "\u044D\u043B\u0435\u043C\u0435\u043D\u0442\u044B", many: "\u044D\u043B\u0435\u043C\u0435\u043D\u0442\u0430\u045E" }, verb: "\u043C\u0435\u0446\u044C" }, file: { unit: { one: "\u0431\u0430\u0439\u0442", few: "\u0431\u0430\u0439\u0442\u044B", many: "\u0431\u0430\u0439\u0442\u0430\u045E" }, verb: "\u043C\u0435\u0446\u044C" } };
  function t(n) {
    return e[n] ?? null;
  }
  let r = (n) => {
    let i = typeof n;
    switch (i) {
      case "number":
        return Number.isNaN(n) ? "NaN" : "\u043B\u0456\u043A";
      case "object": {
        if (Array.isArray(n)) return "\u043C\u0430\u0441\u0456\u045E";
        if (n === null) return "null";
        if (Object.getPrototypeOf(n) !== Object.prototype && n.constructor) return n.constructor.name;
      }
    }
    return i;
  }, o = { regex: "\u0443\u0432\u043E\u0434", email: "email \u0430\u0434\u0440\u0430\u0441", url: "URL", emoji: "\u044D\u043C\u043E\u0434\u0437\u0456", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "ISO \u0434\u0430\u0442\u0430 \u0456 \u0447\u0430\u0441", date: "ISO \u0434\u0430\u0442\u0430", time: "ISO \u0447\u0430\u0441", duration: "ISO \u043F\u0440\u0430\u0446\u044F\u0433\u043B\u0430\u0441\u0446\u044C", ipv4: "IPv4 \u0430\u0434\u0440\u0430\u0441", ipv6: "IPv6 \u0430\u0434\u0440\u0430\u0441", cidrv4: "IPv4 \u0434\u044B\u044F\u043F\u0430\u0437\u043E\u043D", cidrv6: "IPv6 \u0434\u044B\u044F\u043F\u0430\u0437\u043E\u043D", base64: "\u0440\u0430\u0434\u043E\u043A \u0443 \u0444\u0430\u0440\u043C\u0430\u0446\u0435 base64", base64url: "\u0440\u0430\u0434\u043E\u043A \u0443 \u0444\u0430\u0440\u043C\u0430\u0446\u0435 base64url", json_string: "JSON \u0440\u0430\u0434\u043E\u043A", e164: "\u043D\u0443\u043C\u0430\u0440 E.164", jwt: "JWT", template_literal: "\u0443\u0432\u043E\u0434" };
  return (n) => {
    switch (n.code) {
      case "invalid_type":
        return `\u041D\u044F\u043F\u0440\u0430\u0432\u0456\u043B\u044C\u043D\u044B \u045E\u0432\u043E\u0434: \u0447\u0430\u043A\u0430\u045E\u0441\u044F ${n.expected}, \u0430\u0442\u0440\u044B\u043C\u0430\u043D\u0430 ${r(n.input)}`;
      case "invalid_value":
        if (n.values.length === 1) return `\u041D\u044F\u043F\u0440\u0430\u0432\u0456\u043B\u044C\u043D\u044B \u045E\u0432\u043E\u0434: \u0447\u0430\u043A\u0430\u043B\u0430\u0441\u044F ${D(n.values[0])}`;
        return `\u041D\u044F\u043F\u0440\u0430\u0432\u0456\u043B\u044C\u043D\u044B \u0432\u0430\u0440\u044B\u044F\u043D\u0442: \u0447\u0430\u043A\u0430\u045E\u0441\u044F \u0430\u0434\u0437\u0456\u043D \u0437 ${T(n.values, "|")}`;
      case "too_big": {
        let i = n.inclusive ? "<=" : "<", s = t(n.origin);
        if (s) {
          let a = Number(n.maximum), c = J0(a, s.unit.one, s.unit.few, s.unit.many);
          return `\u0417\u0430\u043D\u0430\u0434\u0442\u0430 \u0432\u044F\u043B\u0456\u043A\u0456: \u0447\u0430\u043A\u0430\u043B\u0430\u0441\u044F, \u0448\u0442\u043E ${n.origin ?? "\u0437\u043D\u0430\u0447\u044D\u043D\u043D\u0435"} \u043F\u0430\u0432\u0456\u043D\u043D\u0430 ${s.verb} ${i}${n.maximum.toString()} ${c}`;
        }
        return `\u0417\u0430\u043D\u0430\u0434\u0442\u0430 \u0432\u044F\u043B\u0456\u043A\u0456: \u0447\u0430\u043A\u0430\u043B\u0430\u0441\u044F, \u0448\u0442\u043E ${n.origin ?? "\u0437\u043D\u0430\u0447\u044D\u043D\u043D\u0435"} \u043F\u0430\u0432\u0456\u043D\u043D\u0430 \u0431\u044B\u0446\u044C ${i}${n.maximum.toString()}`;
      }
      case "too_small": {
        let i = n.inclusive ? ">=" : ">", s = t(n.origin);
        if (s) {
          let a = Number(n.minimum), c = J0(a, s.unit.one, s.unit.few, s.unit.many);
          return `\u0417\u0430\u043D\u0430\u0434\u0442\u0430 \u043C\u0430\u043B\u044B: \u0447\u0430\u043A\u0430\u043B\u0430\u0441\u044F, \u0448\u0442\u043E ${n.origin} \u043F\u0430\u0432\u0456\u043D\u043D\u0430 ${s.verb} ${i}${n.minimum.toString()} ${c}`;
        }
        return `\u0417\u0430\u043D\u0430\u0434\u0442\u0430 \u043C\u0430\u043B\u044B: \u0447\u0430\u043A\u0430\u043B\u0430\u0441\u044F, \u0448\u0442\u043E ${n.origin} \u043F\u0430\u0432\u0456\u043D\u043D\u0430 \u0431\u044B\u0446\u044C ${i}${n.minimum.toString()}`;
      }
      case "invalid_format": {
        let i = n;
        if (i.format === "starts_with") return `\u041D\u044F\u043F\u0440\u0430\u0432\u0456\u043B\u044C\u043D\u044B \u0440\u0430\u0434\u043E\u043A: \u043F\u0430\u0432\u0456\u043D\u0435\u043D \u043F\u0430\u0447\u044B\u043D\u0430\u0446\u0446\u0430 \u0437 "${i.prefix}"`;
        if (i.format === "ends_with") return `\u041D\u044F\u043F\u0440\u0430\u0432\u0456\u043B\u044C\u043D\u044B \u0440\u0430\u0434\u043E\u043A: \u043F\u0430\u0432\u0456\u043D\u0435\u043D \u0437\u0430\u043A\u0430\u043D\u0447\u0432\u0430\u0446\u0446\u0430 \u043D\u0430 "${i.suffix}"`;
        if (i.format === "includes") return `\u041D\u044F\u043F\u0440\u0430\u0432\u0456\u043B\u044C\u043D\u044B \u0440\u0430\u0434\u043E\u043A: \u043F\u0430\u0432\u0456\u043D\u0435\u043D \u0437\u043C\u044F\u0448\u0447\u0430\u0446\u044C "${i.includes}"`;
        if (i.format === "regex") return `\u041D\u044F\u043F\u0440\u0430\u0432\u0456\u043B\u044C\u043D\u044B \u0440\u0430\u0434\u043E\u043A: \u043F\u0430\u0432\u0456\u043D\u0435\u043D \u0430\u0434\u043F\u0430\u0432\u044F\u0434\u0430\u0446\u044C \u0448\u0430\u0431\u043B\u043E\u043D\u0443 ${i.pattern}`;
        return `\u041D\u044F\u043F\u0440\u0430\u0432\u0456\u043B\u044C\u043D\u044B ${o[i.format] ?? n.format}`;
      }
      case "not_multiple_of":
        return `\u041D\u044F\u043F\u0440\u0430\u0432\u0456\u043B\u044C\u043D\u044B \u043B\u0456\u043A: \u043F\u0430\u0432\u0456\u043D\u0435\u043D \u0431\u044B\u0446\u044C \u043A\u0440\u0430\u0442\u043D\u044B\u043C ${n.divisor}`;
      case "unrecognized_keys":
        return `\u041D\u0435\u0440\u0430\u0441\u043F\u0430\u0437\u043D\u0430\u043D\u044B ${n.keys.length > 1 ? "\u043A\u043B\u044E\u0447\u044B" : "\u043A\u043B\u044E\u0447"}: ${T(n.keys, ", ")}`;
      case "invalid_key":
        return `\u041D\u044F\u043F\u0440\u0430\u0432\u0456\u043B\u044C\u043D\u044B \u043A\u043B\u044E\u0447 \u0443 ${n.origin}`;
      case "invalid_union":
        return "\u041D\u044F\u043F\u0440\u0430\u0432\u0456\u043B\u044C\u043D\u044B \u045E\u0432\u043E\u0434";
      case "invalid_element":
        return `\u041D\u044F\u043F\u0440\u0430\u0432\u0456\u043B\u044C\u043D\u0430\u0435 \u0437\u043D\u0430\u0447\u044D\u043D\u043D\u0435 \u045E ${n.origin}`;
      default:
        return "\u041D\u044F\u043F\u0440\u0430\u0432\u0456\u043B\u044C\u043D\u044B \u045E\u0432\u043E\u0434";
    }
  };
};
function u_() {
  return { localeError: iV() };
}
var sV = () => {
  let e = { string: { unit: "car\xE0cters", verb: "contenir" }, file: { unit: "bytes", verb: "contenir" }, array: { unit: "elements", verb: "contenir" }, set: { unit: "elements", verb: "contenir" } };
  function t(n) {
    return e[n] ?? null;
  }
  let r = (n) => {
    let i = typeof n;
    switch (i) {
      case "number":
        return Number.isNaN(n) ? "NaN" : "number";
      case "object": {
        if (Array.isArray(n)) return "array";
        if (n === null) return "null";
        if (Object.getPrototypeOf(n) !== Object.prototype && n.constructor) return n.constructor.name;
      }
    }
    return i;
  }, o = { regex: "entrada", email: "adre\xE7a electr\xF2nica", url: "URL", emoji: "emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "data i hora ISO", date: "data ISO", time: "hora ISO", duration: "durada ISO", ipv4: "adre\xE7a IPv4", ipv6: "adre\xE7a IPv6", cidrv4: "rang IPv4", cidrv6: "rang IPv6", base64: "cadena codificada en base64", base64url: "cadena codificada en base64url", json_string: "cadena JSON", e164: "n\xFAmero E.164", jwt: "JWT", template_literal: "entrada" };
  return (n) => {
    switch (n.code) {
      case "invalid_type":
        return `Tipus inv\xE0lid: s'esperava ${n.expected}, s'ha rebut ${r(n.input)}`;
      case "invalid_value":
        if (n.values.length === 1) return `Valor inv\xE0lid: s'esperava ${D(n.values[0])}`;
        return `Opci\xF3 inv\xE0lida: s'esperava una de ${T(n.values, " o ")}`;
      case "too_big": {
        let i = n.inclusive ? "com a m\xE0xim" : "menys de", s = t(n.origin);
        if (s) return `Massa gran: s'esperava que ${n.origin ?? "el valor"} contingu\xE9s ${i} ${n.maximum.toString()} ${s.unit ?? "elements"}`;
        return `Massa gran: s'esperava que ${n.origin ?? "el valor"} fos ${i} ${n.maximum.toString()}`;
      }
      case "too_small": {
        let i = n.inclusive ? "com a m\xEDnim" : "m\xE9s de", s = t(n.origin);
        if (s) return `Massa petit: s'esperava que ${n.origin} contingu\xE9s ${i} ${n.minimum.toString()} ${s.unit}`;
        return `Massa petit: s'esperava que ${n.origin} fos ${i} ${n.minimum.toString()}`;
      }
      case "invalid_format": {
        let i = n;
        if (i.format === "starts_with") return `Format inv\xE0lid: ha de comen\xE7ar amb "${i.prefix}"`;
        if (i.format === "ends_with") return `Format inv\xE0lid: ha d'acabar amb "${i.suffix}"`;
        if (i.format === "includes") return `Format inv\xE0lid: ha d'incloure "${i.includes}"`;
        if (i.format === "regex") return `Format inv\xE0lid: ha de coincidir amb el patr\xF3 ${i.pattern}`;
        return `Format inv\xE0lid per a ${o[i.format] ?? n.format}`;
      }
      case "not_multiple_of":
        return `N\xFAmero inv\xE0lid: ha de ser m\xFAltiple de ${n.divisor}`;
      case "unrecognized_keys":
        return `Clau${n.keys.length > 1 ? "s" : ""} no reconeguda${n.keys.length > 1 ? "s" : ""}: ${T(n.keys, ", ")}`;
      case "invalid_key":
        return `Clau inv\xE0lida a ${n.origin}`;
      case "invalid_union":
        return "Entrada inv\xE0lida";
      case "invalid_element":
        return `Element inv\xE0lid a ${n.origin}`;
      default:
        return "Entrada inv\xE0lida";
    }
  };
};
function d_() {
  return { localeError: sV() };
}
var aV = () => {
  let e = { string: { unit: "znak\u016F", verb: "m\xEDt" }, file: { unit: "bajt\u016F", verb: "m\xEDt" }, array: { unit: "prvk\u016F", verb: "m\xEDt" }, set: { unit: "prvk\u016F", verb: "m\xEDt" } };
  function t(n) {
    return e[n] ?? null;
  }
  let r = (n) => {
    let i = typeof n;
    switch (i) {
      case "number":
        return Number.isNaN(n) ? "NaN" : "\u010D\xEDslo";
      case "string":
        return "\u0159et\u011Bzec";
      case "boolean":
        return "boolean";
      case "bigint":
        return "bigint";
      case "function":
        return "funkce";
      case "symbol":
        return "symbol";
      case "undefined":
        return "undefined";
      case "object": {
        if (Array.isArray(n)) return "pole";
        if (n === null) return "null";
        if (Object.getPrototypeOf(n) !== Object.prototype && n.constructor) return n.constructor.name;
      }
    }
    return i;
  }, o = { regex: "regul\xE1rn\xED v\xFDraz", email: "e-mailov\xE1 adresa", url: "URL", emoji: "emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "datum a \u010Das ve form\xE1tu ISO", date: "datum ve form\xE1tu ISO", time: "\u010Das ve form\xE1tu ISO", duration: "doba trv\xE1n\xED ISO", ipv4: "IPv4 adresa", ipv6: "IPv6 adresa", cidrv4: "rozsah IPv4", cidrv6: "rozsah IPv6", base64: "\u0159et\u011Bzec zak\xF3dovan\xFD ve form\xE1tu base64", base64url: "\u0159et\u011Bzec zak\xF3dovan\xFD ve form\xE1tu base64url", json_string: "\u0159et\u011Bzec ve form\xE1tu JSON", e164: "\u010D\xEDslo E.164", jwt: "JWT", template_literal: "vstup" };
  return (n) => {
    switch (n.code) {
      case "invalid_type":
        return `Neplatn\xFD vstup: o\u010Dek\xE1v\xE1no ${n.expected}, obdr\u017Eeno ${r(n.input)}`;
      case "invalid_value":
        if (n.values.length === 1) return `Neplatn\xFD vstup: o\u010Dek\xE1v\xE1no ${D(n.values[0])}`;
        return `Neplatn\xE1 mo\u017Enost: o\u010Dek\xE1v\xE1na jedna z hodnot ${T(n.values, "|")}`;
      case "too_big": {
        let i = n.inclusive ? "<=" : "<", s = t(n.origin);
        if (s) return `Hodnota je p\u0159\xEDli\u0161 velk\xE1: ${n.origin ?? "hodnota"} mus\xED m\xEDt ${i}${n.maximum.toString()} ${s.unit ?? "prvk\u016F"}`;
        return `Hodnota je p\u0159\xEDli\u0161 velk\xE1: ${n.origin ?? "hodnota"} mus\xED b\xFDt ${i}${n.maximum.toString()}`;
      }
      case "too_small": {
        let i = n.inclusive ? ">=" : ">", s = t(n.origin);
        if (s) return `Hodnota je p\u0159\xEDli\u0161 mal\xE1: ${n.origin ?? "hodnota"} mus\xED m\xEDt ${i}${n.minimum.toString()} ${s.unit ?? "prvk\u016F"}`;
        return `Hodnota je p\u0159\xEDli\u0161 mal\xE1: ${n.origin ?? "hodnota"} mus\xED b\xFDt ${i}${n.minimum.toString()}`;
      }
      case "invalid_format": {
        let i = n;
        if (i.format === "starts_with") return `Neplatn\xFD \u0159et\u011Bzec: mus\xED za\u010D\xEDnat na "${i.prefix}"`;
        if (i.format === "ends_with") return `Neplatn\xFD \u0159et\u011Bzec: mus\xED kon\u010Dit na "${i.suffix}"`;
        if (i.format === "includes") return `Neplatn\xFD \u0159et\u011Bzec: mus\xED obsahovat "${i.includes}"`;
        if (i.format === "regex") return `Neplatn\xFD \u0159et\u011Bzec: mus\xED odpov\xEDdat vzoru ${i.pattern}`;
        return `Neplatn\xFD form\xE1t ${o[i.format] ?? n.format}`;
      }
      case "not_multiple_of":
        return `Neplatn\xE9 \u010D\xEDslo: mus\xED b\xFDt n\xE1sobkem ${n.divisor}`;
      case "unrecognized_keys":
        return `Nezn\xE1m\xE9 kl\xED\u010De: ${T(n.keys, ", ")}`;
      case "invalid_key":
        return `Neplatn\xFD kl\xED\u010D v ${n.origin}`;
      case "invalid_union":
        return "Neplatn\xFD vstup";
      case "invalid_element":
        return `Neplatn\xE1 hodnota v ${n.origin}`;
      default:
        return "Neplatn\xFD vstup";
    }
  };
};
function p_() {
  return { localeError: aV() };
}
var cV = () => {
  let e = { string: { unit: "Zeichen", verb: "zu haben" }, file: { unit: "Bytes", verb: "zu haben" }, array: { unit: "Elemente", verb: "zu haben" }, set: { unit: "Elemente", verb: "zu haben" } };
  function t(n) {
    return e[n] ?? null;
  }
  let r = (n) => {
    let i = typeof n;
    switch (i) {
      case "number":
        return Number.isNaN(n) ? "NaN" : "Zahl";
      case "object": {
        if (Array.isArray(n)) return "Array";
        if (n === null) return "null";
        if (Object.getPrototypeOf(n) !== Object.prototype && n.constructor) return n.constructor.name;
      }
    }
    return i;
  }, o = { regex: "Eingabe", email: "E-Mail-Adresse", url: "URL", emoji: "Emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "ISO-Datum und -Uhrzeit", date: "ISO-Datum", time: "ISO-Uhrzeit", duration: "ISO-Dauer", ipv4: "IPv4-Adresse", ipv6: "IPv6-Adresse", cidrv4: "IPv4-Bereich", cidrv6: "IPv6-Bereich", base64: "Base64-codierter String", base64url: "Base64-URL-codierter String", json_string: "JSON-String", e164: "E.164-Nummer", jwt: "JWT", template_literal: "Eingabe" };
  return (n) => {
    switch (n.code) {
      case "invalid_type":
        return `Ung\xFCltige Eingabe: erwartet ${n.expected}, erhalten ${r(n.input)}`;
      case "invalid_value":
        if (n.values.length === 1) return `Ung\xFCltige Eingabe: erwartet ${D(n.values[0])}`;
        return `Ung\xFCltige Option: erwartet eine von ${T(n.values, "|")}`;
      case "too_big": {
        let i = n.inclusive ? "<=" : "<", s = t(n.origin);
        if (s) return `Zu gro\xDF: erwartet, dass ${n.origin ?? "Wert"} ${i}${n.maximum.toString()} ${s.unit ?? "Elemente"} hat`;
        return `Zu gro\xDF: erwartet, dass ${n.origin ?? "Wert"} ${i}${n.maximum.toString()} ist`;
      }
      case "too_small": {
        let i = n.inclusive ? ">=" : ">", s = t(n.origin);
        if (s) return `Zu klein: erwartet, dass ${n.origin} ${i}${n.minimum.toString()} ${s.unit} hat`;
        return `Zu klein: erwartet, dass ${n.origin} ${i}${n.minimum.toString()} ist`;
      }
      case "invalid_format": {
        let i = n;
        if (i.format === "starts_with") return `Ung\xFCltiger String: muss mit "${i.prefix}" beginnen`;
        if (i.format === "ends_with") return `Ung\xFCltiger String: muss mit "${i.suffix}" enden`;
        if (i.format === "includes") return `Ung\xFCltiger String: muss "${i.includes}" enthalten`;
        if (i.format === "regex") return `Ung\xFCltiger String: muss dem Muster ${i.pattern} entsprechen`;
        return `Ung\xFCltig: ${o[i.format] ?? n.format}`;
      }
      case "not_multiple_of":
        return `Ung\xFCltige Zahl: muss ein Vielfaches von ${n.divisor} sein`;
      case "unrecognized_keys":
        return `${n.keys.length > 1 ? "Unbekannte Schl\xFCssel" : "Unbekannter Schl\xFCssel"}: ${T(n.keys, ", ")}`;
      case "invalid_key":
        return `Ung\xFCltiger Schl\xFCssel in ${n.origin}`;
      case "invalid_union":
        return "Ung\xFCltige Eingabe";
      case "invalid_element":
        return `Ung\xFCltiger Wert in ${n.origin}`;
      default:
        return "Ung\xFCltige Eingabe";
    }
  };
};
function f_() {
  return { localeError: cV() };
}
var lV = (e) => {
  let t = typeof e;
  switch (t) {
    case "number":
      return Number.isNaN(e) ? "NaN" : "number";
    case "object": {
      if (Array.isArray(e)) return "array";
      if (e === null) return "null";
      if (Object.getPrototypeOf(e) !== Object.prototype && e.constructor) return e.constructor.name;
    }
  }
  return t;
};
var uV = () => {
  let e = { string: { unit: "characters", verb: "to have" }, file: { unit: "bytes", verb: "to have" }, array: { unit: "items", verb: "to have" }, set: { unit: "items", verb: "to have" } };
  function t(o) {
    return e[o] ?? null;
  }
  let r = { regex: "input", email: "email address", url: "URL", emoji: "emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "ISO datetime", date: "ISO date", time: "ISO time", duration: "ISO duration", ipv4: "IPv4 address", ipv6: "IPv6 address", cidrv4: "IPv4 range", cidrv6: "IPv6 range", base64: "base64-encoded string", base64url: "base64url-encoded string", json_string: "JSON string", e164: "E.164 number", jwt: "JWT", template_literal: "input" };
  return (o) => {
    switch (o.code) {
      case "invalid_type":
        return `Invalid input: expected ${o.expected}, received ${lV(o.input)}`;
      case "invalid_value":
        if (o.values.length === 1) return `Invalid input: expected ${D(o.values[0])}`;
        return `Invalid option: expected one of ${T(o.values, "|")}`;
      case "too_big": {
        let n = o.inclusive ? "<=" : "<", i = t(o.origin);
        if (i) return `Too big: expected ${o.origin ?? "value"} to have ${n}${o.maximum.toString()} ${i.unit ?? "elements"}`;
        return `Too big: expected ${o.origin ?? "value"} to be ${n}${o.maximum.toString()}`;
      }
      case "too_small": {
        let n = o.inclusive ? ">=" : ">", i = t(o.origin);
        if (i) return `Too small: expected ${o.origin} to have ${n}${o.minimum.toString()} ${i.unit}`;
        return `Too small: expected ${o.origin} to be ${n}${o.minimum.toString()}`;
      }
      case "invalid_format": {
        let n = o;
        if (n.format === "starts_with") return `Invalid string: must start with "${n.prefix}"`;
        if (n.format === "ends_with") return `Invalid string: must end with "${n.suffix}"`;
        if (n.format === "includes") return `Invalid string: must include "${n.includes}"`;
        if (n.format === "regex") return `Invalid string: must match pattern ${n.pattern}`;
        return `Invalid ${r[n.format] ?? o.format}`;
      }
      case "not_multiple_of":
        return `Invalid number: must be a multiple of ${o.divisor}`;
      case "unrecognized_keys":
        return `Unrecognized key${o.keys.length > 1 ? "s" : ""}: ${T(o.keys, ", ")}`;
      case "invalid_key":
        return `Invalid key in ${o.origin}`;
      case "invalid_union":
        return "Invalid input";
      case "invalid_element":
        return `Invalid value in ${o.origin}`;
      default:
        return "Invalid input";
    }
  };
};
function Rc() {
  return { localeError: uV() };
}
var dV = (e) => {
  let t = typeof e;
  switch (t) {
    case "number":
      return Number.isNaN(e) ? "NaN" : "nombro";
    case "object": {
      if (Array.isArray(e)) return "tabelo";
      if (e === null) return "senvalora";
      if (Object.getPrototypeOf(e) !== Object.prototype && e.constructor) return e.constructor.name;
    }
  }
  return t;
};
var pV = () => {
  let e = { string: { unit: "karaktrojn", verb: "havi" }, file: { unit: "bajtojn", verb: "havi" }, array: { unit: "elementojn", verb: "havi" }, set: { unit: "elementojn", verb: "havi" } };
  function t(o) {
    return e[o] ?? null;
  }
  let r = { regex: "enigo", email: "retadreso", url: "URL", emoji: "emo\u011Dio", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "ISO-datotempo", date: "ISO-dato", time: "ISO-tempo", duration: "ISO-da\u016Dro", ipv4: "IPv4-adreso", ipv6: "IPv6-adreso", cidrv4: "IPv4-rango", cidrv6: "IPv6-rango", base64: "64-ume kodita karaktraro", base64url: "URL-64-ume kodita karaktraro", json_string: "JSON-karaktraro", e164: "E.164-nombro", jwt: "JWT", template_literal: "enigo" };
  return (o) => {
    switch (o.code) {
      case "invalid_type":
        return `Nevalida enigo: atendi\u011Dis ${o.expected}, ricevi\u011Dis ${dV(o.input)}`;
      case "invalid_value":
        if (o.values.length === 1) return `Nevalida enigo: atendi\u011Dis ${D(o.values[0])}`;
        return `Nevalida opcio: atendi\u011Dis unu el ${T(o.values, "|")}`;
      case "too_big": {
        let n = o.inclusive ? "<=" : "<", i = t(o.origin);
        if (i) return `Tro granda: atendi\u011Dis ke ${o.origin ?? "valoro"} havu ${n}${o.maximum.toString()} ${i.unit ?? "elementojn"}`;
        return `Tro granda: atendi\u011Dis ke ${o.origin ?? "valoro"} havu ${n}${o.maximum.toString()}`;
      }
      case "too_small": {
        let n = o.inclusive ? ">=" : ">", i = t(o.origin);
        if (i) return `Tro malgranda: atendi\u011Dis ke ${o.origin} havu ${n}${o.minimum.toString()} ${i.unit}`;
        return `Tro malgranda: atendi\u011Dis ke ${o.origin} estu ${n}${o.minimum.toString()}`;
      }
      case "invalid_format": {
        let n = o;
        if (n.format === "starts_with") return `Nevalida karaktraro: devas komenci\u011Di per "${n.prefix}"`;
        if (n.format === "ends_with") return `Nevalida karaktraro: devas fini\u011Di per "${n.suffix}"`;
        if (n.format === "includes") return `Nevalida karaktraro: devas inkluzivi "${n.includes}"`;
        if (n.format === "regex") return `Nevalida karaktraro: devas kongrui kun la modelo ${n.pattern}`;
        return `Nevalida ${r[n.format] ?? o.format}`;
      }
      case "not_multiple_of":
        return `Nevalida nombro: devas esti oblo de ${o.divisor}`;
      case "unrecognized_keys":
        return `Nekonata${o.keys.length > 1 ? "j" : ""} \u015Dlosilo${o.keys.length > 1 ? "j" : ""}: ${T(o.keys, ", ")}`;
      case "invalid_key":
        return `Nevalida \u015Dlosilo en ${o.origin}`;
      case "invalid_union":
        return "Nevalida enigo";
      case "invalid_element":
        return `Nevalida valoro en ${o.origin}`;
      default:
        return "Nevalida enigo";
    }
  };
};
function m_() {
  return { localeError: pV() };
}
var fV = () => {
  let e = { string: { unit: "caracteres", verb: "tener" }, file: { unit: "bytes", verb: "tener" }, array: { unit: "elementos", verb: "tener" }, set: { unit: "elementos", verb: "tener" } };
  function t(n) {
    return e[n] ?? null;
  }
  let r = (n) => {
    let i = typeof n;
    switch (i) {
      case "number":
        return Number.isNaN(n) ? "NaN" : "n\xFAmero";
      case "object": {
        if (Array.isArray(n)) return "arreglo";
        if (n === null) return "nulo";
        if (Object.getPrototypeOf(n) !== Object.prototype) return n.constructor.name;
      }
    }
    return i;
  }, o = { regex: "entrada", email: "direcci\xF3n de correo electr\xF3nico", url: "URL", emoji: "emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "fecha y hora ISO", date: "fecha ISO", time: "hora ISO", duration: "duraci\xF3n ISO", ipv4: "direcci\xF3n IPv4", ipv6: "direcci\xF3n IPv6", cidrv4: "rango IPv4", cidrv6: "rango IPv6", base64: "cadena codificada en base64", base64url: "URL codificada en base64", json_string: "cadena JSON", e164: "n\xFAmero E.164", jwt: "JWT", template_literal: "entrada" };
  return (n) => {
    switch (n.code) {
      case "invalid_type":
        return `Entrada inv\xE1lida: se esperaba ${n.expected}, recibido ${r(n.input)}`;
      case "invalid_value":
        if (n.values.length === 1) return `Entrada inv\xE1lida: se esperaba ${D(n.values[0])}`;
        return `Opci\xF3n inv\xE1lida: se esperaba una de ${T(n.values, "|")}`;
      case "too_big": {
        let i = n.inclusive ? "<=" : "<", s = t(n.origin);
        if (s) return `Demasiado grande: se esperaba que ${n.origin ?? "valor"} tuviera ${i}${n.maximum.toString()} ${s.unit ?? "elementos"}`;
        return `Demasiado grande: se esperaba que ${n.origin ?? "valor"} fuera ${i}${n.maximum.toString()}`;
      }
      case "too_small": {
        let i = n.inclusive ? ">=" : ">", s = t(n.origin);
        if (s) return `Demasiado peque\xF1o: se esperaba que ${n.origin} tuviera ${i}${n.minimum.toString()} ${s.unit}`;
        return `Demasiado peque\xF1o: se esperaba que ${n.origin} fuera ${i}${n.minimum.toString()}`;
      }
      case "invalid_format": {
        let i = n;
        if (i.format === "starts_with") return `Cadena inv\xE1lida: debe comenzar con "${i.prefix}"`;
        if (i.format === "ends_with") return `Cadena inv\xE1lida: debe terminar en "${i.suffix}"`;
        if (i.format === "includes") return `Cadena inv\xE1lida: debe incluir "${i.includes}"`;
        if (i.format === "regex") return `Cadena inv\xE1lida: debe coincidir con el patr\xF3n ${i.pattern}`;
        return `Inv\xE1lido ${o[i.format] ?? n.format}`;
      }
      case "not_multiple_of":
        return `N\xFAmero inv\xE1lido: debe ser m\xFAltiplo de ${n.divisor}`;
      case "unrecognized_keys":
        return `Llave${n.keys.length > 1 ? "s" : ""} desconocida${n.keys.length > 1 ? "s" : ""}: ${T(n.keys, ", ")}`;
      case "invalid_key":
        return `Llave inv\xE1lida en ${n.origin}`;
      case "invalid_union":
        return "Entrada inv\xE1lida";
      case "invalid_element":
        return `Valor inv\xE1lido en ${n.origin}`;
      default:
        return "Entrada inv\xE1lida";
    }
  };
};
function g_() {
  return { localeError: fV() };
}
var mV = () => {
  let e = { string: { unit: "\u06A9\u0627\u0631\u0627\u06A9\u062A\u0631", verb: "\u062F\u0627\u0634\u062A\u0647 \u0628\u0627\u0634\u062F" }, file: { unit: "\u0628\u0627\u06CC\u062A", verb: "\u062F\u0627\u0634\u062A\u0647 \u0628\u0627\u0634\u062F" }, array: { unit: "\u0622\u06CC\u062A\u0645", verb: "\u062F\u0627\u0634\u062A\u0647 \u0628\u0627\u0634\u062F" }, set: { unit: "\u0622\u06CC\u062A\u0645", verb: "\u062F\u0627\u0634\u062A\u0647 \u0628\u0627\u0634\u062F" } };
  function t(n) {
    return e[n] ?? null;
  }
  let r = (n) => {
    let i = typeof n;
    switch (i) {
      case "number":
        return Number.isNaN(n) ? "NaN" : "\u0639\u062F\u062F";
      case "object": {
        if (Array.isArray(n)) return "\u0622\u0631\u0627\u06CC\u0647";
        if (n === null) return "null";
        if (Object.getPrototypeOf(n) !== Object.prototype && n.constructor) return n.constructor.name;
      }
    }
    return i;
  }, o = { regex: "\u0648\u0631\u0648\u062F\u06CC", email: "\u0622\u062F\u0631\u0633 \u0627\u06CC\u0645\u06CC\u0644", url: "URL", emoji: "\u0627\u06CC\u0645\u0648\u062C\u06CC", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "\u062A\u0627\u0631\u06CC\u062E \u0648 \u0632\u0645\u0627\u0646 \u0627\u06CC\u0632\u0648", date: "\u062A\u0627\u0631\u06CC\u062E \u0627\u06CC\u0632\u0648", time: "\u0632\u0645\u0627\u0646 \u0627\u06CC\u0632\u0648", duration: "\u0645\u062F\u062A \u0632\u0645\u0627\u0646 \u0627\u06CC\u0632\u0648", ipv4: "IPv4 \u0622\u062F\u0631\u0633", ipv6: "IPv6 \u0622\u062F\u0631\u0633", cidrv4: "IPv4 \u062F\u0627\u0645\u0646\u0647", cidrv6: "IPv6 \u062F\u0627\u0645\u0646\u0647", base64: "base64-encoded \u0631\u0634\u062A\u0647", base64url: "base64url-encoded \u0631\u0634\u062A\u0647", json_string: "JSON \u0631\u0634\u062A\u0647", e164: "E.164 \u0639\u062F\u062F", jwt: "JWT", template_literal: "\u0648\u0631\u0648\u062F\u06CC" };
  return (n) => {
    switch (n.code) {
      case "invalid_type":
        return `\u0648\u0631\u0648\u062F\u06CC \u0646\u0627\u0645\u0639\u062A\u0628\u0631: \u0645\u06CC\u200C\u0628\u0627\u06CC\u0633\u062A ${n.expected} \u0645\u06CC\u200C\u0628\u0648\u062F\u060C ${r(n.input)} \u062F\u0631\u06CC\u0627\u0641\u062A \u0634\u062F`;
      case "invalid_value":
        if (n.values.length === 1) return `\u0648\u0631\u0648\u062F\u06CC \u0646\u0627\u0645\u0639\u062A\u0628\u0631: \u0645\u06CC\u200C\u0628\u0627\u06CC\u0633\u062A ${D(n.values[0])} \u0645\u06CC\u200C\u0628\u0648\u062F`;
        return `\u06AF\u0632\u06CC\u0646\u0647 \u0646\u0627\u0645\u0639\u062A\u0628\u0631: \u0645\u06CC\u200C\u0628\u0627\u06CC\u0633\u062A \u06CC\u06A9\u06CC \u0627\u0632 ${T(n.values, "|")} \u0645\u06CC\u200C\u0628\u0648\u062F`;
      case "too_big": {
        let i = n.inclusive ? "<=" : "<", s = t(n.origin);
        if (s) return `\u062E\u06CC\u0644\u06CC \u0628\u0632\u0631\u06AF: ${n.origin ?? "\u0645\u0642\u062F\u0627\u0631"} \u0628\u0627\u06CC\u062F ${i}${n.maximum.toString()} ${s.unit ?? "\u0639\u0646\u0635\u0631"} \u0628\u0627\u0634\u062F`;
        return `\u062E\u06CC\u0644\u06CC \u0628\u0632\u0631\u06AF: ${n.origin ?? "\u0645\u0642\u062F\u0627\u0631"} \u0628\u0627\u06CC\u062F ${i}${n.maximum.toString()} \u0628\u0627\u0634\u062F`;
      }
      case "too_small": {
        let i = n.inclusive ? ">=" : ">", s = t(n.origin);
        if (s) return `\u062E\u06CC\u0644\u06CC \u06A9\u0648\u0686\u06A9: ${n.origin} \u0628\u0627\u06CC\u062F ${i}${n.minimum.toString()} ${s.unit} \u0628\u0627\u0634\u062F`;
        return `\u062E\u06CC\u0644\u06CC \u06A9\u0648\u0686\u06A9: ${n.origin} \u0628\u0627\u06CC\u062F ${i}${n.minimum.toString()} \u0628\u0627\u0634\u062F`;
      }
      case "invalid_format": {
        let i = n;
        if (i.format === "starts_with") return `\u0631\u0634\u062A\u0647 \u0646\u0627\u0645\u0639\u062A\u0628\u0631: \u0628\u0627\u06CC\u062F \u0628\u0627 "${i.prefix}" \u0634\u0631\u0648\u0639 \u0634\u0648\u062F`;
        if (i.format === "ends_with") return `\u0631\u0634\u062A\u0647 \u0646\u0627\u0645\u0639\u062A\u0628\u0631: \u0628\u0627\u06CC\u062F \u0628\u0627 "${i.suffix}" \u062A\u0645\u0627\u0645 \u0634\u0648\u062F`;
        if (i.format === "includes") return `\u0631\u0634\u062A\u0647 \u0646\u0627\u0645\u0639\u062A\u0628\u0631: \u0628\u0627\u06CC\u062F \u0634\u0627\u0645\u0644 "${i.includes}" \u0628\u0627\u0634\u062F`;
        if (i.format === "regex") return `\u0631\u0634\u062A\u0647 \u0646\u0627\u0645\u0639\u062A\u0628\u0631: \u0628\u0627\u06CC\u062F \u0628\u0627 \u0627\u0644\u06AF\u0648\u06CC ${i.pattern} \u0645\u0637\u0627\u0628\u0642\u062A \u062F\u0627\u0634\u062A\u0647 \u0628\u0627\u0634\u062F`;
        return `${o[i.format] ?? n.format} \u0646\u0627\u0645\u0639\u062A\u0628\u0631`;
      }
      case "not_multiple_of":
        return `\u0639\u062F\u062F \u0646\u0627\u0645\u0639\u062A\u0628\u0631: \u0628\u0627\u06CC\u062F \u0645\u0636\u0631\u0628 ${n.divisor} \u0628\u0627\u0634\u062F`;
      case "unrecognized_keys":
        return `\u06A9\u0644\u06CC\u062F${n.keys.length > 1 ? "\u0647\u0627\u06CC" : ""} \u0646\u0627\u0634\u0646\u0627\u0633: ${T(n.keys, ", ")}`;
      case "invalid_key":
        return `\u06A9\u0644\u06CC\u062F \u0646\u0627\u0634\u0646\u0627\u0633 \u062F\u0631 ${n.origin}`;
      case "invalid_union":
        return "\u0648\u0631\u0648\u062F\u06CC \u0646\u0627\u0645\u0639\u062A\u0628\u0631";
      case "invalid_element":
        return `\u0645\u0642\u062F\u0627\u0631 \u0646\u0627\u0645\u0639\u062A\u0628\u0631 \u062F\u0631 ${n.origin}`;
      default:
        return "\u0648\u0631\u0648\u062F\u06CC \u0646\u0627\u0645\u0639\u062A\u0628\u0631";
    }
  };
};
function h_() {
  return { localeError: mV() };
}
var gV = () => {
  let e = { string: { unit: "merkki\xE4", subject: "merkkijonon" }, file: { unit: "tavua", subject: "tiedoston" }, array: { unit: "alkiota", subject: "listan" }, set: { unit: "alkiota", subject: "joukon" }, number: { unit: "", subject: "luvun" }, bigint: { unit: "", subject: "suuren kokonaisluvun" }, int: { unit: "", subject: "kokonaisluvun" }, date: { unit: "", subject: "p\xE4iv\xE4m\xE4\xE4r\xE4n" } };
  function t(n) {
    return e[n] ?? null;
  }
  let r = (n) => {
    let i = typeof n;
    switch (i) {
      case "number":
        return Number.isNaN(n) ? "NaN" : "number";
      case "object": {
        if (Array.isArray(n)) return "array";
        if (n === null) return "null";
        if (Object.getPrototypeOf(n) !== Object.prototype && n.constructor) return n.constructor.name;
      }
    }
    return i;
  }, o = { regex: "s\xE4\xE4nn\xF6llinen lauseke", email: "s\xE4hk\xF6postiosoite", url: "URL-osoite", emoji: "emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "ISO-aikaleima", date: "ISO-p\xE4iv\xE4m\xE4\xE4r\xE4", time: "ISO-aika", duration: "ISO-kesto", ipv4: "IPv4-osoite", ipv6: "IPv6-osoite", cidrv4: "IPv4-alue", cidrv6: "IPv6-alue", base64: "base64-koodattu merkkijono", base64url: "base64url-koodattu merkkijono", json_string: "JSON-merkkijono", e164: "E.164-luku", jwt: "JWT", template_literal: "templaattimerkkijono" };
  return (n) => {
    switch (n.code) {
      case "invalid_type":
        return `Virheellinen tyyppi: odotettiin ${n.expected}, oli ${r(n.input)}`;
      case "invalid_value":
        if (n.values.length === 1) return `Virheellinen sy\xF6te: t\xE4ytyy olla ${D(n.values[0])}`;
        return `Virheellinen valinta: t\xE4ytyy olla yksi seuraavista: ${T(n.values, "|")}`;
      case "too_big": {
        let i = n.inclusive ? "<=" : "<", s = t(n.origin);
        if (s) return `Liian suuri: ${s.subject} t\xE4ytyy olla ${i}${n.maximum.toString()} ${s.unit}`.trim();
        return `Liian suuri: arvon t\xE4ytyy olla ${i}${n.maximum.toString()}`;
      }
      case "too_small": {
        let i = n.inclusive ? ">=" : ">", s = t(n.origin);
        if (s) return `Liian pieni: ${s.subject} t\xE4ytyy olla ${i}${n.minimum.toString()} ${s.unit}`.trim();
        return `Liian pieni: arvon t\xE4ytyy olla ${i}${n.minimum.toString()}`;
      }
      case "invalid_format": {
        let i = n;
        if (i.format === "starts_with") return `Virheellinen sy\xF6te: t\xE4ytyy alkaa "${i.prefix}"`;
        if (i.format === "ends_with") return `Virheellinen sy\xF6te: t\xE4ytyy loppua "${i.suffix}"`;
        if (i.format === "includes") return `Virheellinen sy\xF6te: t\xE4ytyy sis\xE4lt\xE4\xE4 "${i.includes}"`;
        if (i.format === "regex") return `Virheellinen sy\xF6te: t\xE4ytyy vastata s\xE4\xE4nn\xF6llist\xE4 lauseketta ${i.pattern}`;
        return `Virheellinen ${o[i.format] ?? n.format}`;
      }
      case "not_multiple_of":
        return `Virheellinen luku: t\xE4ytyy olla luvun ${n.divisor} monikerta`;
      case "unrecognized_keys":
        return `${n.keys.length > 1 ? "Tuntemattomat avaimet" : "Tuntematon avain"}: ${T(n.keys, ", ")}`;
      case "invalid_key":
        return "Virheellinen avain tietueessa";
      case "invalid_union":
        return "Virheellinen unioni";
      case "invalid_element":
        return "Virheellinen arvo joukossa";
      default:
        return "Virheellinen sy\xF6te";
    }
  };
};
function y_() {
  return { localeError: gV() };
}
var hV = () => {
  let e = { string: { unit: "caract\xE8res", verb: "avoir" }, file: { unit: "octets", verb: "avoir" }, array: { unit: "\xE9l\xE9ments", verb: "avoir" }, set: { unit: "\xE9l\xE9ments", verb: "avoir" } };
  function t(n) {
    return e[n] ?? null;
  }
  let r = (n) => {
    let i = typeof n;
    switch (i) {
      case "number":
        return Number.isNaN(n) ? "NaN" : "nombre";
      case "object": {
        if (Array.isArray(n)) return "tableau";
        if (n === null) return "null";
        if (Object.getPrototypeOf(n) !== Object.prototype && n.constructor) return n.constructor.name;
      }
    }
    return i;
  }, o = { regex: "entr\xE9e", email: "adresse e-mail", url: "URL", emoji: "emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "date et heure ISO", date: "date ISO", time: "heure ISO", duration: "dur\xE9e ISO", ipv4: "adresse IPv4", ipv6: "adresse IPv6", cidrv4: "plage IPv4", cidrv6: "plage IPv6", base64: "cha\xEEne encod\xE9e en base64", base64url: "cha\xEEne encod\xE9e en base64url", json_string: "cha\xEEne JSON", e164: "num\xE9ro E.164", jwt: "JWT", template_literal: "entr\xE9e" };
  return (n) => {
    switch (n.code) {
      case "invalid_type":
        return `Entr\xE9e invalide : ${n.expected} attendu, ${r(n.input)} re\xE7u`;
      case "invalid_value":
        if (n.values.length === 1) return `Entr\xE9e invalide : ${D(n.values[0])} attendu`;
        return `Option invalide : une valeur parmi ${T(n.values, "|")} attendue`;
      case "too_big": {
        let i = n.inclusive ? "<=" : "<", s = t(n.origin);
        if (s) return `Trop grand : ${n.origin ?? "valeur"} doit ${s.verb} ${i}${n.maximum.toString()} ${s.unit ?? "\xE9l\xE9ment(s)"}`;
        return `Trop grand : ${n.origin ?? "valeur"} doit \xEAtre ${i}${n.maximum.toString()}`;
      }
      case "too_small": {
        let i = n.inclusive ? ">=" : ">", s = t(n.origin);
        if (s) return `Trop petit : ${n.origin} doit ${s.verb} ${i}${n.minimum.toString()} ${s.unit}`;
        return `Trop petit : ${n.origin} doit \xEAtre ${i}${n.minimum.toString()}`;
      }
      case "invalid_format": {
        let i = n;
        if (i.format === "starts_with") return `Cha\xEEne invalide : doit commencer par "${i.prefix}"`;
        if (i.format === "ends_with") return `Cha\xEEne invalide : doit se terminer par "${i.suffix}"`;
        if (i.format === "includes") return `Cha\xEEne invalide : doit inclure "${i.includes}"`;
        if (i.format === "regex") return `Cha\xEEne invalide : doit correspondre au mod\xE8le ${i.pattern}`;
        return `${o[i.format] ?? n.format} invalide`;
      }
      case "not_multiple_of":
        return `Nombre invalide : doit \xEAtre un multiple de ${n.divisor}`;
      case "unrecognized_keys":
        return `Cl\xE9${n.keys.length > 1 ? "s" : ""} non reconnue${n.keys.length > 1 ? "s" : ""} : ${T(n.keys, ", ")}`;
      case "invalid_key":
        return `Cl\xE9 invalide dans ${n.origin}`;
      case "invalid_union":
        return "Entr\xE9e invalide";
      case "invalid_element":
        return `Valeur invalide dans ${n.origin}`;
      default:
        return "Entr\xE9e invalide";
    }
  };
};
function b_() {
  return { localeError: hV() };
}
var yV = () => {
  let e = { string: { unit: "caract\xE8res", verb: "avoir" }, file: { unit: "octets", verb: "avoir" }, array: { unit: "\xE9l\xE9ments", verb: "avoir" }, set: { unit: "\xE9l\xE9ments", verb: "avoir" } };
  function t(n) {
    return e[n] ?? null;
  }
  let r = (n) => {
    let i = typeof n;
    switch (i) {
      case "number":
        return Number.isNaN(n) ? "NaN" : "number";
      case "object": {
        if (Array.isArray(n)) return "array";
        if (n === null) return "null";
        if (Object.getPrototypeOf(n) !== Object.prototype && n.constructor) return n.constructor.name;
      }
    }
    return i;
  }, o = { regex: "entr\xE9e", email: "adresse courriel", url: "URL", emoji: "emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "date-heure ISO", date: "date ISO", time: "heure ISO", duration: "dur\xE9e ISO", ipv4: "adresse IPv4", ipv6: "adresse IPv6", cidrv4: "plage IPv4", cidrv6: "plage IPv6", base64: "cha\xEEne encod\xE9e en base64", base64url: "cha\xEEne encod\xE9e en base64url", json_string: "cha\xEEne JSON", e164: "num\xE9ro E.164", jwt: "JWT", template_literal: "entr\xE9e" };
  return (n) => {
    switch (n.code) {
      case "invalid_type":
        return `Entr\xE9e invalide : attendu ${n.expected}, re\xE7u ${r(n.input)}`;
      case "invalid_value":
        if (n.values.length === 1) return `Entr\xE9e invalide : attendu ${D(n.values[0])}`;
        return `Option invalide : attendu l'une des valeurs suivantes ${T(n.values, "|")}`;
      case "too_big": {
        let i = n.inclusive ? "\u2264" : "<", s = t(n.origin);
        if (s) return `Trop grand : attendu que ${n.origin ?? "la valeur"} ait ${i}${n.maximum.toString()} ${s.unit}`;
        return `Trop grand : attendu que ${n.origin ?? "la valeur"} soit ${i}${n.maximum.toString()}`;
      }
      case "too_small": {
        let i = n.inclusive ? "\u2265" : ">", s = t(n.origin);
        if (s) return `Trop petit : attendu que ${n.origin} ait ${i}${n.minimum.toString()} ${s.unit}`;
        return `Trop petit : attendu que ${n.origin} soit ${i}${n.minimum.toString()}`;
      }
      case "invalid_format": {
        let i = n;
        if (i.format === "starts_with") return `Cha\xEEne invalide : doit commencer par "${i.prefix}"`;
        if (i.format === "ends_with") return `Cha\xEEne invalide : doit se terminer par "${i.suffix}"`;
        if (i.format === "includes") return `Cha\xEEne invalide : doit inclure "${i.includes}"`;
        if (i.format === "regex") return `Cha\xEEne invalide : doit correspondre au motif ${i.pattern}`;
        return `${o[i.format] ?? n.format} invalide`;
      }
      case "not_multiple_of":
        return `Nombre invalide : doit \xEAtre un multiple de ${n.divisor}`;
      case "unrecognized_keys":
        return `Cl\xE9${n.keys.length > 1 ? "s" : ""} non reconnue${n.keys.length > 1 ? "s" : ""} : ${T(n.keys, ", ")}`;
      case "invalid_key":
        return `Cl\xE9 invalide dans ${n.origin}`;
      case "invalid_union":
        return "Entr\xE9e invalide";
      case "invalid_element":
        return `Valeur invalide dans ${n.origin}`;
      default:
        return "Entr\xE9e invalide";
    }
  };
};
function __() {
  return { localeError: yV() };
}
var bV = () => {
  let e = { string: { unit: "\u05D0\u05D5\u05EA\u05D9\u05D5\u05EA", verb: "\u05DC\u05DB\u05DC\u05D5\u05DC" }, file: { unit: "\u05D1\u05D9\u05D9\u05D8\u05D9\u05DD", verb: "\u05DC\u05DB\u05DC\u05D5\u05DC" }, array: { unit: "\u05E4\u05E8\u05D9\u05D8\u05D9\u05DD", verb: "\u05DC\u05DB\u05DC\u05D5\u05DC" }, set: { unit: "\u05E4\u05E8\u05D9\u05D8\u05D9\u05DD", verb: "\u05DC\u05DB\u05DC\u05D5\u05DC" } };
  function t(n) {
    return e[n] ?? null;
  }
  let r = (n) => {
    let i = typeof n;
    switch (i) {
      case "number":
        return Number.isNaN(n) ? "NaN" : "number";
      case "object": {
        if (Array.isArray(n)) return "array";
        if (n === null) return "null";
        if (Object.getPrototypeOf(n) !== Object.prototype && n.constructor) return n.constructor.name;
      }
    }
    return i;
  }, o = { regex: "\u05E7\u05DC\u05D8", email: "\u05DB\u05EA\u05D5\u05D1\u05EA \u05D0\u05D9\u05DE\u05D9\u05D9\u05DC", url: "\u05DB\u05EA\u05D5\u05D1\u05EA \u05E8\u05E9\u05EA", emoji: "\u05D0\u05D9\u05DE\u05D5\u05D2'\u05D9", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "\u05EA\u05D0\u05E8\u05D9\u05DA \u05D5\u05D6\u05DE\u05DF ISO", date: "\u05EA\u05D0\u05E8\u05D9\u05DA ISO", time: "\u05D6\u05DE\u05DF ISO", duration: "\u05DE\u05E9\u05DA \u05D6\u05DE\u05DF ISO", ipv4: "\u05DB\u05EA\u05D5\u05D1\u05EA IPv4", ipv6: "\u05DB\u05EA\u05D5\u05D1\u05EA IPv6", cidrv4: "\u05D8\u05D5\u05D5\u05D7 IPv4", cidrv6: "\u05D8\u05D5\u05D5\u05D7 IPv6", base64: "\u05DE\u05D7\u05E8\u05D5\u05D6\u05EA \u05D1\u05D1\u05E1\u05D9\u05E1 64", base64url: "\u05DE\u05D7\u05E8\u05D5\u05D6\u05EA \u05D1\u05D1\u05E1\u05D9\u05E1 64 \u05DC\u05DB\u05EA\u05D5\u05D1\u05D5\u05EA \u05E8\u05E9\u05EA", json_string: "\u05DE\u05D7\u05E8\u05D5\u05D6\u05EA JSON", e164: "\u05DE\u05E1\u05E4\u05E8 E.164", jwt: "JWT", template_literal: "\u05E7\u05DC\u05D8" };
  return (n) => {
    switch (n.code) {
      case "invalid_type":
        return `\u05E7\u05DC\u05D8 \u05DC\u05D0 \u05EA\u05E7\u05D9\u05DF: \u05E6\u05E8\u05D9\u05DA ${n.expected}, \u05D4\u05EA\u05E7\u05D1\u05DC ${r(n.input)}`;
      case "invalid_value":
        if (n.values.length === 1) return `\u05E7\u05DC\u05D8 \u05DC\u05D0 \u05EA\u05E7\u05D9\u05DF: \u05E6\u05E8\u05D9\u05DA ${D(n.values[0])}`;
        return `\u05E7\u05DC\u05D8 \u05DC\u05D0 \u05EA\u05E7\u05D9\u05DF: \u05E6\u05E8\u05D9\u05DA \u05D0\u05D7\u05EA \u05DE\u05D4\u05D0\u05E4\u05E9\u05E8\u05D5\u05D9\u05D5\u05EA  ${T(n.values, "|")}`;
      case "too_big": {
        let i = n.inclusive ? "<=" : "<", s = t(n.origin);
        if (s) return `\u05D2\u05D3\u05D5\u05DC \u05DE\u05D3\u05D9: ${n.origin ?? "value"} \u05E6\u05E8\u05D9\u05DA \u05DC\u05D4\u05D9\u05D5\u05EA ${i}${n.maximum.toString()} ${s.unit ?? "elements"}`;
        return `\u05D2\u05D3\u05D5\u05DC \u05DE\u05D3\u05D9: ${n.origin ?? "value"} \u05E6\u05E8\u05D9\u05DA \u05DC\u05D4\u05D9\u05D5\u05EA ${i}${n.maximum.toString()}`;
      }
      case "too_small": {
        let i = n.inclusive ? ">=" : ">", s = t(n.origin);
        if (s) return `\u05E7\u05D8\u05DF \u05DE\u05D3\u05D9: ${n.origin} \u05E6\u05E8\u05D9\u05DA \u05DC\u05D4\u05D9\u05D5\u05EA ${i}${n.minimum.toString()} ${s.unit}`;
        return `\u05E7\u05D8\u05DF \u05DE\u05D3\u05D9: ${n.origin} \u05E6\u05E8\u05D9\u05DA \u05DC\u05D4\u05D9\u05D5\u05EA ${i}${n.minimum.toString()}`;
      }
      case "invalid_format": {
        let i = n;
        if (i.format === "starts_with") return `\u05DE\u05D7\u05E8\u05D5\u05D6\u05EA \u05DC\u05D0 \u05EA\u05E7\u05D9\u05E0\u05D4: \u05D7\u05D9\u05D9\u05D1\u05EA \u05DC\u05D4\u05EA\u05D7\u05D9\u05DC \u05D1"${i.prefix}"`;
        if (i.format === "ends_with") return `\u05DE\u05D7\u05E8\u05D5\u05D6\u05EA \u05DC\u05D0 \u05EA\u05E7\u05D9\u05E0\u05D4: \u05D7\u05D9\u05D9\u05D1\u05EA \u05DC\u05D4\u05E1\u05EA\u05D9\u05D9\u05DD \u05D1 "${i.suffix}"`;
        if (i.format === "includes") return `\u05DE\u05D7\u05E8\u05D5\u05D6\u05EA \u05DC\u05D0 \u05EA\u05E7\u05D9\u05E0\u05D4: \u05D7\u05D9\u05D9\u05D1\u05EA \u05DC\u05DB\u05DC\u05D5\u05DC "${i.includes}"`;
        if (i.format === "regex") return `\u05DE\u05D7\u05E8\u05D5\u05D6\u05EA \u05DC\u05D0 \u05EA\u05E7\u05D9\u05E0\u05D4: \u05D7\u05D9\u05D9\u05D1\u05EA \u05DC\u05D4\u05EA\u05D0\u05D9\u05DD \u05DC\u05EA\u05D1\u05E0\u05D9\u05EA ${i.pattern}`;
        return `${o[i.format] ?? n.format} \u05DC\u05D0 \u05EA\u05E7\u05D9\u05DF`;
      }
      case "not_multiple_of":
        return `\u05DE\u05E1\u05E4\u05E8 \u05DC\u05D0 \u05EA\u05E7\u05D9\u05DF: \u05D7\u05D9\u05D9\u05D1 \u05DC\u05D4\u05D9\u05D5\u05EA \u05DE\u05DB\u05E4\u05DC\u05D4 \u05E9\u05DC ${n.divisor}`;
      case "unrecognized_keys":
        return `\u05DE\u05E4\u05EA\u05D7${n.keys.length > 1 ? "\u05D5\u05EA" : ""} \u05DC\u05D0 \u05DE\u05D6\u05D5\u05D4${n.keys.length > 1 ? "\u05D9\u05DD" : "\u05D4"}: ${T(n.keys, ", ")}`;
      case "invalid_key":
        return `\u05DE\u05E4\u05EA\u05D7 \u05DC\u05D0 \u05EA\u05E7\u05D9\u05DF \u05D1${n.origin}`;
      case "invalid_union":
        return "\u05E7\u05DC\u05D8 \u05DC\u05D0 \u05EA\u05E7\u05D9\u05DF";
      case "invalid_element":
        return `\u05E2\u05E8\u05DA \u05DC\u05D0 \u05EA\u05E7\u05D9\u05DF \u05D1${n.origin}`;
      default:
        return "\u05E7\u05DC\u05D8 \u05DC\u05D0 \u05EA\u05E7\u05D9\u05DF";
    }
  };
};
function v_() {
  return { localeError: bV() };
}
var _V = () => {
  let e = { string: { unit: "karakter", verb: "legyen" }, file: { unit: "byte", verb: "legyen" }, array: { unit: "elem", verb: "legyen" }, set: { unit: "elem", verb: "legyen" } };
  function t(n) {
    return e[n] ?? null;
  }
  let r = (n) => {
    let i = typeof n;
    switch (i) {
      case "number":
        return Number.isNaN(n) ? "NaN" : "sz\xE1m";
      case "object": {
        if (Array.isArray(n)) return "t\xF6mb";
        if (n === null) return "null";
        if (Object.getPrototypeOf(n) !== Object.prototype && n.constructor) return n.constructor.name;
      }
    }
    return i;
  }, o = { regex: "bemenet", email: "email c\xEDm", url: "URL", emoji: "emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "ISO id\u0151b\xE9lyeg", date: "ISO d\xE1tum", time: "ISO id\u0151", duration: "ISO id\u0151intervallum", ipv4: "IPv4 c\xEDm", ipv6: "IPv6 c\xEDm", cidrv4: "IPv4 tartom\xE1ny", cidrv6: "IPv6 tartom\xE1ny", base64: "base64-k\xF3dolt string", base64url: "base64url-k\xF3dolt string", json_string: "JSON string", e164: "E.164 sz\xE1m", jwt: "JWT", template_literal: "bemenet" };
  return (n) => {
    switch (n.code) {
      case "invalid_type":
        return `\xC9rv\xE9nytelen bemenet: a v\xE1rt \xE9rt\xE9k ${n.expected}, a kapott \xE9rt\xE9k ${r(n.input)}`;
      case "invalid_value":
        if (n.values.length === 1) return `\xC9rv\xE9nytelen bemenet: a v\xE1rt \xE9rt\xE9k ${D(n.values[0])}`;
        return `\xC9rv\xE9nytelen opci\xF3: valamelyik \xE9rt\xE9k v\xE1rt ${T(n.values, "|")}`;
      case "too_big": {
        let i = n.inclusive ? "<=" : "<", s = t(n.origin);
        if (s) return `T\xFAl nagy: ${n.origin ?? "\xE9rt\xE9k"} m\xE9rete t\xFAl nagy ${i}${n.maximum.toString()} ${s.unit ?? "elem"}`;
        return `T\xFAl nagy: a bemeneti \xE9rt\xE9k ${n.origin ?? "\xE9rt\xE9k"} t\xFAl nagy: ${i}${n.maximum.toString()}`;
      }
      case "too_small": {
        let i = n.inclusive ? ">=" : ">", s = t(n.origin);
        if (s) return `T\xFAl kicsi: a bemeneti \xE9rt\xE9k ${n.origin} m\xE9rete t\xFAl kicsi ${i}${n.minimum.toString()} ${s.unit}`;
        return `T\xFAl kicsi: a bemeneti \xE9rt\xE9k ${n.origin} t\xFAl kicsi ${i}${n.minimum.toString()}`;
      }
      case "invalid_format": {
        let i = n;
        if (i.format === "starts_with") return `\xC9rv\xE9nytelen string: "${i.prefix}" \xE9rt\xE9kkel kell kezd\u0151dnie`;
        if (i.format === "ends_with") return `\xC9rv\xE9nytelen string: "${i.suffix}" \xE9rt\xE9kkel kell v\xE9gz\u0151dnie`;
        if (i.format === "includes") return `\xC9rv\xE9nytelen string: "${i.includes}" \xE9rt\xE9ket kell tartalmaznia`;
        if (i.format === "regex") return `\xC9rv\xE9nytelen string: ${i.pattern} mint\xE1nak kell megfelelnie`;
        return `\xC9rv\xE9nytelen ${o[i.format] ?? n.format}`;
      }
      case "not_multiple_of":
        return `\xC9rv\xE9nytelen sz\xE1m: ${n.divisor} t\xF6bbsz\xF6r\xF6s\xE9nek kell lennie`;
      case "unrecognized_keys":
        return `Ismeretlen kulcs${n.keys.length > 1 ? "s" : ""}: ${T(n.keys, ", ")}`;
      case "invalid_key":
        return `\xC9rv\xE9nytelen kulcs ${n.origin}`;
      case "invalid_union":
        return "\xC9rv\xE9nytelen bemenet";
      case "invalid_element":
        return `\xC9rv\xE9nytelen \xE9rt\xE9k: ${n.origin}`;
      default:
        return "\xC9rv\xE9nytelen bemenet";
    }
  };
};
function S_() {
  return { localeError: _V() };
}
var vV = () => {
  let e = { string: { unit: "karakter", verb: "memiliki" }, file: { unit: "byte", verb: "memiliki" }, array: { unit: "item", verb: "memiliki" }, set: { unit: "item", verb: "memiliki" } };
  function t(n) {
    return e[n] ?? null;
  }
  let r = (n) => {
    let i = typeof n;
    switch (i) {
      case "number":
        return Number.isNaN(n) ? "NaN" : "number";
      case "object": {
        if (Array.isArray(n)) return "array";
        if (n === null) return "null";
        if (Object.getPrototypeOf(n) !== Object.prototype && n.constructor) return n.constructor.name;
      }
    }
    return i;
  }, o = { regex: "input", email: "alamat email", url: "URL", emoji: "emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "tanggal dan waktu format ISO", date: "tanggal format ISO", time: "jam format ISO", duration: "durasi format ISO", ipv4: "alamat IPv4", ipv6: "alamat IPv6", cidrv4: "rentang alamat IPv4", cidrv6: "rentang alamat IPv6", base64: "string dengan enkode base64", base64url: "string dengan enkode base64url", json_string: "string JSON", e164: "angka E.164", jwt: "JWT", template_literal: "input" };
  return (n) => {
    switch (n.code) {
      case "invalid_type":
        return `Input tidak valid: diharapkan ${n.expected}, diterima ${r(n.input)}`;
      case "invalid_value":
        if (n.values.length === 1) return `Input tidak valid: diharapkan ${D(n.values[0])}`;
        return `Pilihan tidak valid: diharapkan salah satu dari ${T(n.values, "|")}`;
      case "too_big": {
        let i = n.inclusive ? "<=" : "<", s = t(n.origin);
        if (s) return `Terlalu besar: diharapkan ${n.origin ?? "value"} memiliki ${i}${n.maximum.toString()} ${s.unit ?? "elemen"}`;
        return `Terlalu besar: diharapkan ${n.origin ?? "value"} menjadi ${i}${n.maximum.toString()}`;
      }
      case "too_small": {
        let i = n.inclusive ? ">=" : ">", s = t(n.origin);
        if (s) return `Terlalu kecil: diharapkan ${n.origin} memiliki ${i}${n.minimum.toString()} ${s.unit}`;
        return `Terlalu kecil: diharapkan ${n.origin} menjadi ${i}${n.minimum.toString()}`;
      }
      case "invalid_format": {
        let i = n;
        if (i.format === "starts_with") return `String tidak valid: harus dimulai dengan "${i.prefix}"`;
        if (i.format === "ends_with") return `String tidak valid: harus berakhir dengan "${i.suffix}"`;
        if (i.format === "includes") return `String tidak valid: harus menyertakan "${i.includes}"`;
        if (i.format === "regex") return `String tidak valid: harus sesuai pola ${i.pattern}`;
        return `${o[i.format] ?? n.format} tidak valid`;
      }
      case "not_multiple_of":
        return `Angka tidak valid: harus kelipatan dari ${n.divisor}`;
      case "unrecognized_keys":
        return `Kunci tidak dikenali ${n.keys.length > 1 ? "s" : ""}: ${T(n.keys, ", ")}`;
      case "invalid_key":
        return `Kunci tidak valid di ${n.origin}`;
      case "invalid_union":
        return "Input tidak valid";
      case "invalid_element":
        return `Nilai tidak valid di ${n.origin}`;
      default:
        return "Input tidak valid";
    }
  };
};
function x_() {
  return { localeError: vV() };
}
var SV = () => {
  let e = { string: { unit: "caratteri", verb: "avere" }, file: { unit: "byte", verb: "avere" }, array: { unit: "elementi", verb: "avere" }, set: { unit: "elementi", verb: "avere" } };
  function t(n) {
    return e[n] ?? null;
  }
  let r = (n) => {
    let i = typeof n;
    switch (i) {
      case "number":
        return Number.isNaN(n) ? "NaN" : "numero";
      case "object": {
        if (Array.isArray(n)) return "vettore";
        if (n === null) return "null";
        if (Object.getPrototypeOf(n) !== Object.prototype && n.constructor) return n.constructor.name;
      }
    }
    return i;
  }, o = { regex: "input", email: "indirizzo email", url: "URL", emoji: "emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "data e ora ISO", date: "data ISO", time: "ora ISO", duration: "durata ISO", ipv4: "indirizzo IPv4", ipv6: "indirizzo IPv6", cidrv4: "intervallo IPv4", cidrv6: "intervallo IPv6", base64: "stringa codificata in base64", base64url: "URL codificata in base64", json_string: "stringa JSON", e164: "numero E.164", jwt: "JWT", template_literal: "input" };
  return (n) => {
    switch (n.code) {
      case "invalid_type":
        return `Input non valido: atteso ${n.expected}, ricevuto ${r(n.input)}`;
      case "invalid_value":
        if (n.values.length === 1) return `Input non valido: atteso ${D(n.values[0])}`;
        return `Opzione non valida: atteso uno tra ${T(n.values, "|")}`;
      case "too_big": {
        let i = n.inclusive ? "<=" : "<", s = t(n.origin);
        if (s) return `Troppo grande: ${n.origin ?? "valore"} deve avere ${i}${n.maximum.toString()} ${s.unit ?? "elementi"}`;
        return `Troppo grande: ${n.origin ?? "valore"} deve essere ${i}${n.maximum.toString()}`;
      }
      case "too_small": {
        let i = n.inclusive ? ">=" : ">", s = t(n.origin);
        if (s) return `Troppo piccolo: ${n.origin} deve avere ${i}${n.minimum.toString()} ${s.unit}`;
        return `Troppo piccolo: ${n.origin} deve essere ${i}${n.minimum.toString()}`;
      }
      case "invalid_format": {
        let i = n;
        if (i.format === "starts_with") return `Stringa non valida: deve iniziare con "${i.prefix}"`;
        if (i.format === "ends_with") return `Stringa non valida: deve terminare con "${i.suffix}"`;
        if (i.format === "includes") return `Stringa non valida: deve includere "${i.includes}"`;
        if (i.format === "regex") return `Stringa non valida: deve corrispondere al pattern ${i.pattern}`;
        return `Invalid ${o[i.format] ?? n.format}`;
      }
      case "not_multiple_of":
        return `Numero non valido: deve essere un multiplo di ${n.divisor}`;
      case "unrecognized_keys":
        return `Chiav${n.keys.length > 1 ? "i" : "e"} non riconosciut${n.keys.length > 1 ? "e" : "a"}: ${T(n.keys, ", ")}`;
      case "invalid_key":
        return `Chiave non valida in ${n.origin}`;
      case "invalid_union":
        return "Input non valido";
      case "invalid_element":
        return `Valore non valido in ${n.origin}`;
      default:
        return "Input non valido";
    }
  };
};
function w_() {
  return { localeError: SV() };
}
var xV = () => {
  let e = { string: { unit: "\u6587\u5B57", verb: "\u3067\u3042\u308B" }, file: { unit: "\u30D0\u30A4\u30C8", verb: "\u3067\u3042\u308B" }, array: { unit: "\u8981\u7D20", verb: "\u3067\u3042\u308B" }, set: { unit: "\u8981\u7D20", verb: "\u3067\u3042\u308B" } };
  function t(n) {
    return e[n] ?? null;
  }
  let r = (n) => {
    let i = typeof n;
    switch (i) {
      case "number":
        return Number.isNaN(n) ? "NaN" : "\u6570\u5024";
      case "object": {
        if (Array.isArray(n)) return "\u914D\u5217";
        if (n === null) return "null";
        if (Object.getPrototypeOf(n) !== Object.prototype && n.constructor) return n.constructor.name;
      }
    }
    return i;
  }, o = { regex: "\u5165\u529B\u5024", email: "\u30E1\u30FC\u30EB\u30A2\u30C9\u30EC\u30B9", url: "URL", emoji: "\u7D75\u6587\u5B57", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "ISO\u65E5\u6642", date: "ISO\u65E5\u4ED8", time: "ISO\u6642\u523B", duration: "ISO\u671F\u9593", ipv4: "IPv4\u30A2\u30C9\u30EC\u30B9", ipv6: "IPv6\u30A2\u30C9\u30EC\u30B9", cidrv4: "IPv4\u7BC4\u56F2", cidrv6: "IPv6\u7BC4\u56F2", base64: "base64\u30A8\u30F3\u30B3\u30FC\u30C9\u6587\u5B57\u5217", base64url: "base64url\u30A8\u30F3\u30B3\u30FC\u30C9\u6587\u5B57\u5217", json_string: "JSON\u6587\u5B57\u5217", e164: "E.164\u756A\u53F7", jwt: "JWT", template_literal: "\u5165\u529B\u5024" };
  return (n) => {
    switch (n.code) {
      case "invalid_type":
        return `\u7121\u52B9\u306A\u5165\u529B: ${n.expected}\u304C\u671F\u5F85\u3055\u308C\u307E\u3057\u305F\u304C\u3001${r(n.input)}\u304C\u5165\u529B\u3055\u308C\u307E\u3057\u305F`;
      case "invalid_value":
        if (n.values.length === 1) return `\u7121\u52B9\u306A\u5165\u529B: ${D(n.values[0])}\u304C\u671F\u5F85\u3055\u308C\u307E\u3057\u305F`;
        return `\u7121\u52B9\u306A\u9078\u629E: ${T(n.values, "\u3001")}\u306E\u3044\u305A\u308C\u304B\u3067\u3042\u308B\u5FC5\u8981\u304C\u3042\u308A\u307E\u3059`;
      case "too_big": {
        let i = n.inclusive ? "\u4EE5\u4E0B\u3067\u3042\u308B" : "\u3088\u308A\u5C0F\u3055\u3044", s = t(n.origin);
        if (s) return `\u5927\u304D\u3059\u304E\u308B\u5024: ${n.origin ?? "\u5024"}\u306F${n.maximum.toString()}${s.unit ?? "\u8981\u7D20"}${i}\u5FC5\u8981\u304C\u3042\u308A\u307E\u3059`;
        return `\u5927\u304D\u3059\u304E\u308B\u5024: ${n.origin ?? "\u5024"}\u306F${n.maximum.toString()}${i}\u5FC5\u8981\u304C\u3042\u308A\u307E\u3059`;
      }
      case "too_small": {
        let i = n.inclusive ? "\u4EE5\u4E0A\u3067\u3042\u308B" : "\u3088\u308A\u5927\u304D\u3044", s = t(n.origin);
        if (s) return `\u5C0F\u3055\u3059\u304E\u308B\u5024: ${n.origin}\u306F${n.minimum.toString()}${s.unit}${i}\u5FC5\u8981\u304C\u3042\u308A\u307E\u3059`;
        return `\u5C0F\u3055\u3059\u304E\u308B\u5024: ${n.origin}\u306F${n.minimum.toString()}${i}\u5FC5\u8981\u304C\u3042\u308A\u307E\u3059`;
      }
      case "invalid_format": {
        let i = n;
        if (i.format === "starts_with") return `\u7121\u52B9\u306A\u6587\u5B57\u5217: "${i.prefix}"\u3067\u59CB\u307E\u308B\u5FC5\u8981\u304C\u3042\u308A\u307E\u3059`;
        if (i.format === "ends_with") return `\u7121\u52B9\u306A\u6587\u5B57\u5217: "${i.suffix}"\u3067\u7D42\u308F\u308B\u5FC5\u8981\u304C\u3042\u308A\u307E\u3059`;
        if (i.format === "includes") return `\u7121\u52B9\u306A\u6587\u5B57\u5217: "${i.includes}"\u3092\u542B\u3080\u5FC5\u8981\u304C\u3042\u308A\u307E\u3059`;
        if (i.format === "regex") return `\u7121\u52B9\u306A\u6587\u5B57\u5217: \u30D1\u30BF\u30FC\u30F3${i.pattern}\u306B\u4E00\u81F4\u3059\u308B\u5FC5\u8981\u304C\u3042\u308A\u307E\u3059`;
        return `\u7121\u52B9\u306A${o[i.format] ?? n.format}`;
      }
      case "not_multiple_of":
        return `\u7121\u52B9\u306A\u6570\u5024: ${n.divisor}\u306E\u500D\u6570\u3067\u3042\u308B\u5FC5\u8981\u304C\u3042\u308A\u307E\u3059`;
      case "unrecognized_keys":
        return `\u8A8D\u8B58\u3055\u308C\u3066\u3044\u306A\u3044\u30AD\u30FC${n.keys.length > 1 ? "\u7FA4" : ""}: ${T(n.keys, "\u3001")}`;
      case "invalid_key":
        return `${n.origin}\u5185\u306E\u7121\u52B9\u306A\u30AD\u30FC`;
      case "invalid_union":
        return "\u7121\u52B9\u306A\u5165\u529B";
      case "invalid_element":
        return `${n.origin}\u5185\u306E\u7121\u52B9\u306A\u5024`;
      default:
        return "\u7121\u52B9\u306A\u5165\u529B";
    }
  };
};
function k_() {
  return { localeError: xV() };
}
var wV = () => {
  let e = { string: { unit: "\u178F\u17BD\u17A2\u1780\u17D2\u179F\u179A", verb: "\u1782\u17BD\u179A\u1798\u17B6\u1793" }, file: { unit: "\u1794\u17C3", verb: "\u1782\u17BD\u179A\u1798\u17B6\u1793" }, array: { unit: "\u1792\u17B6\u178F\u17BB", verb: "\u1782\u17BD\u179A\u1798\u17B6\u1793" }, set: { unit: "\u1792\u17B6\u178F\u17BB", verb: "\u1782\u17BD\u179A\u1798\u17B6\u1793" } };
  function t(n) {
    return e[n] ?? null;
  }
  let r = (n) => {
    let i = typeof n;
    switch (i) {
      case "number":
        return Number.isNaN(n) ? "\u1798\u17B7\u1793\u1798\u17C2\u1793\u1787\u17B6\u179B\u17C1\u1781 (NaN)" : "\u179B\u17C1\u1781";
      case "object": {
        if (Array.isArray(n)) return "\u17A2\u17B6\u179A\u17C1 (Array)";
        if (n === null) return "\u1782\u17D2\u1798\u17B6\u1793\u178F\u1798\u17D2\u179B\u17C3 (null)";
        if (Object.getPrototypeOf(n) !== Object.prototype && n.constructor) return n.constructor.name;
      }
    }
    return i;
  }, o = { regex: "\u1791\u17B7\u1793\u17D2\u1793\u1793\u17D0\u1799\u1794\u1789\u17D2\u1785\u17BC\u179B", email: "\u17A2\u17B6\u179F\u1799\u178A\u17D2\u178B\u17B6\u1793\u17A2\u17CA\u17B8\u1798\u17C2\u179B", url: "URL", emoji: "\u179F\u1789\u17D2\u1789\u17B6\u17A2\u17B6\u179A\u1798\u17D2\u1798\u178E\u17CD", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "\u1780\u17B6\u179B\u1794\u179A\u17B7\u1785\u17D2\u1786\u17C1\u1791 \u1793\u17B7\u1784\u1798\u17C9\u17C4\u1784 ISO", date: "\u1780\u17B6\u179B\u1794\u179A\u17B7\u1785\u17D2\u1786\u17C1\u1791 ISO", time: "\u1798\u17C9\u17C4\u1784 ISO", duration: "\u179A\u1799\u17C8\u1796\u17C1\u179B ISO", ipv4: "\u17A2\u17B6\u179F\u1799\u178A\u17D2\u178B\u17B6\u1793 IPv4", ipv6: "\u17A2\u17B6\u179F\u1799\u178A\u17D2\u178B\u17B6\u1793 IPv6", cidrv4: "\u178A\u17C2\u1793\u17A2\u17B6\u179F\u1799\u178A\u17D2\u178B\u17B6\u1793 IPv4", cidrv6: "\u178A\u17C2\u1793\u17A2\u17B6\u179F\u1799\u178A\u17D2\u178B\u17B6\u1793 IPv6", base64: "\u1781\u17D2\u179F\u17C2\u17A2\u1780\u17D2\u179F\u179A\u17A2\u17CA\u17B7\u1780\u17BC\u178A base64", base64url: "\u1781\u17D2\u179F\u17C2\u17A2\u1780\u17D2\u179F\u179A\u17A2\u17CA\u17B7\u1780\u17BC\u178A base64url", json_string: "\u1781\u17D2\u179F\u17C2\u17A2\u1780\u17D2\u179F\u179A JSON", e164: "\u179B\u17C1\u1781 E.164", jwt: "JWT", template_literal: "\u1791\u17B7\u1793\u17D2\u1793\u1793\u17D0\u1799\u1794\u1789\u17D2\u1785\u17BC\u179B" };
  return (n) => {
    switch (n.code) {
      case "invalid_type":
        return `\u1791\u17B7\u1793\u17D2\u1793\u1793\u17D0\u1799\u1794\u1789\u17D2\u1785\u17BC\u179B\u1798\u17B7\u1793\u178F\u17D2\u179A\u17B9\u1798\u178F\u17D2\u179A\u17BC\u179C\u17D6 \u178F\u17D2\u179A\u17BC\u179C\u1780\u17B6\u179A ${n.expected} \u1794\u17C9\u17BB\u1793\u17D2\u178F\u17C2\u1791\u1791\u17BD\u179B\u1794\u17B6\u1793 ${r(n.input)}`;
      case "invalid_value":
        if (n.values.length === 1) return `\u1791\u17B7\u1793\u17D2\u1793\u1793\u17D0\u1799\u1794\u1789\u17D2\u1785\u17BC\u179B\u1798\u17B7\u1793\u178F\u17D2\u179A\u17B9\u1798\u178F\u17D2\u179A\u17BC\u179C\u17D6 \u178F\u17D2\u179A\u17BC\u179C\u1780\u17B6\u179A ${D(n.values[0])}`;
        return `\u1787\u1798\u17D2\u179A\u17BE\u179F\u1798\u17B7\u1793\u178F\u17D2\u179A\u17B9\u1798\u178F\u17D2\u179A\u17BC\u179C\u17D6 \u178F\u17D2\u179A\u17BC\u179C\u1787\u17B6\u1798\u17BD\u1799\u1780\u17D2\u1793\u17BB\u1784\u1785\u17C6\u178E\u17C4\u1798 ${T(n.values, "|")}`;
      case "too_big": {
        let i = n.inclusive ? "<=" : "<", s = t(n.origin);
        if (s) return `\u1792\u17C6\u1796\u17C1\u1780\u17D6 \u178F\u17D2\u179A\u17BC\u179C\u1780\u17B6\u179A ${n.origin ?? "\u178F\u1798\u17D2\u179B\u17C3"} ${i} ${n.maximum.toString()} ${s.unit ?? "\u1792\u17B6\u178F\u17BB"}`;
        return `\u1792\u17C6\u1796\u17C1\u1780\u17D6 \u178F\u17D2\u179A\u17BC\u179C\u1780\u17B6\u179A ${n.origin ?? "\u178F\u1798\u17D2\u179B\u17C3"} ${i} ${n.maximum.toString()}`;
      }
      case "too_small": {
        let i = n.inclusive ? ">=" : ">", s = t(n.origin);
        if (s) return `\u178F\u17BC\u1785\u1796\u17C1\u1780\u17D6 \u178F\u17D2\u179A\u17BC\u179C\u1780\u17B6\u179A ${n.origin} ${i} ${n.minimum.toString()} ${s.unit}`;
        return `\u178F\u17BC\u1785\u1796\u17C1\u1780\u17D6 \u178F\u17D2\u179A\u17BC\u179C\u1780\u17B6\u179A ${n.origin} ${i} ${n.minimum.toString()}`;
      }
      case "invalid_format": {
        let i = n;
        if (i.format === "starts_with") return `\u1781\u17D2\u179F\u17C2\u17A2\u1780\u17D2\u179F\u179A\u1798\u17B7\u1793\u178F\u17D2\u179A\u17B9\u1798\u178F\u17D2\u179A\u17BC\u179C\u17D6 \u178F\u17D2\u179A\u17BC\u179C\u1785\u17B6\u1794\u17CB\u1795\u17D2\u178F\u17BE\u1798\u178A\u17C4\u1799 "${i.prefix}"`;
        if (i.format === "ends_with") return `\u1781\u17D2\u179F\u17C2\u17A2\u1780\u17D2\u179F\u179A\u1798\u17B7\u1793\u178F\u17D2\u179A\u17B9\u1798\u178F\u17D2\u179A\u17BC\u179C\u17D6 \u178F\u17D2\u179A\u17BC\u179C\u1794\u1789\u17D2\u1785\u1794\u17CB\u178A\u17C4\u1799 "${i.suffix}"`;
        if (i.format === "includes") return `\u1781\u17D2\u179F\u17C2\u17A2\u1780\u17D2\u179F\u179A\u1798\u17B7\u1793\u178F\u17D2\u179A\u17B9\u1798\u178F\u17D2\u179A\u17BC\u179C\u17D6 \u178F\u17D2\u179A\u17BC\u179C\u1798\u17B6\u1793 "${i.includes}"`;
        if (i.format === "regex") return `\u1781\u17D2\u179F\u17C2\u17A2\u1780\u17D2\u179F\u179A\u1798\u17B7\u1793\u178F\u17D2\u179A\u17B9\u1798\u178F\u17D2\u179A\u17BC\u179C\u17D6 \u178F\u17D2\u179A\u17BC\u179C\u178F\u17C2\u1795\u17D2\u1782\u17BC\u1795\u17D2\u1782\u1784\u1793\u17B9\u1784\u1791\u1798\u17D2\u179A\u1784\u17CB\u178A\u17C2\u179B\u1794\u17B6\u1793\u1780\u17C6\u178E\u178F\u17CB ${i.pattern}`;
        return `\u1798\u17B7\u1793\u178F\u17D2\u179A\u17B9\u1798\u178F\u17D2\u179A\u17BC\u179C\u17D6 ${o[i.format] ?? n.format}`;
      }
      case "not_multiple_of":
        return `\u179B\u17C1\u1781\u1798\u17B7\u1793\u178F\u17D2\u179A\u17B9\u1798\u178F\u17D2\u179A\u17BC\u179C\u17D6 \u178F\u17D2\u179A\u17BC\u179C\u178F\u17C2\u1787\u17B6\u1796\u17A0\u17BB\u1782\u17BB\u178E\u1793\u17C3 ${n.divisor}`;
      case "unrecognized_keys":
        return `\u179A\u1780\u1783\u17BE\u1789\u179F\u17C4\u1798\u17B7\u1793\u179F\u17D2\u1782\u17B6\u179B\u17CB\u17D6 ${T(n.keys, ", ")}`;
      case "invalid_key":
        return `\u179F\u17C4\u1798\u17B7\u1793\u178F\u17D2\u179A\u17B9\u1798\u178F\u17D2\u179A\u17BC\u179C\u1793\u17C5\u1780\u17D2\u1793\u17BB\u1784 ${n.origin}`;
      case "invalid_union":
        return "\u1791\u17B7\u1793\u17D2\u1793\u1793\u17D0\u1799\u1798\u17B7\u1793\u178F\u17D2\u179A\u17B9\u1798\u178F\u17D2\u179A\u17BC\u179C";
      case "invalid_element":
        return `\u1791\u17B7\u1793\u17D2\u1793\u1793\u17D0\u1799\u1798\u17B7\u1793\u178F\u17D2\u179A\u17B9\u1798\u178F\u17D2\u179A\u17BC\u179C\u1793\u17C5\u1780\u17D2\u1793\u17BB\u1784 ${n.origin}`;
      default:
        return "\u1791\u17B7\u1793\u17D2\u1793\u1793\u17D0\u1799\u1798\u17B7\u1793\u178F\u17D2\u179A\u17B9\u1798\u178F\u17D2\u179A\u17BC\u179C";
    }
  };
};
function E_() {
  return { localeError: wV() };
}
var kV = () => {
  let e = { string: { unit: "\uBB38\uC790", verb: "to have" }, file: { unit: "\uBC14\uC774\uD2B8", verb: "to have" }, array: { unit: "\uAC1C", verb: "to have" }, set: { unit: "\uAC1C", verb: "to have" } };
  function t(n) {
    return e[n] ?? null;
  }
  let r = (n) => {
    let i = typeof n;
    switch (i) {
      case "number":
        return Number.isNaN(n) ? "NaN" : "number";
      case "object": {
        if (Array.isArray(n)) return "array";
        if (n === null) return "null";
        if (Object.getPrototypeOf(n) !== Object.prototype && n.constructor) return n.constructor.name;
      }
    }
    return i;
  }, o = { regex: "\uC785\uB825", email: "\uC774\uBA54\uC77C \uC8FC\uC18C", url: "URL", emoji: "\uC774\uBAA8\uC9C0", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "ISO \uB0A0\uC9DC\uC2DC\uAC04", date: "ISO \uB0A0\uC9DC", time: "ISO \uC2DC\uAC04", duration: "ISO \uAE30\uAC04", ipv4: "IPv4 \uC8FC\uC18C", ipv6: "IPv6 \uC8FC\uC18C", cidrv4: "IPv4 \uBC94\uC704", cidrv6: "IPv6 \uBC94\uC704", base64: "base64 \uC778\uCF54\uB529 \uBB38\uC790\uC5F4", base64url: "base64url \uC778\uCF54\uB529 \uBB38\uC790\uC5F4", json_string: "JSON \uBB38\uC790\uC5F4", e164: "E.164 \uBC88\uD638", jwt: "JWT", template_literal: "\uC785\uB825" };
  return (n) => {
    switch (n.code) {
      case "invalid_type":
        return `\uC798\uBABB\uB41C \uC785\uB825: \uC608\uC0C1 \uD0C0\uC785\uC740 ${n.expected}, \uBC1B\uC740 \uD0C0\uC785\uC740 ${r(n.input)}\uC785\uB2C8\uB2E4`;
      case "invalid_value":
        if (n.values.length === 1) return `\uC798\uBABB\uB41C \uC785\uB825: \uAC12\uC740 ${D(n.values[0])} \uC774\uC5B4\uC57C \uD569\uB2C8\uB2E4`;
        return `\uC798\uBABB\uB41C \uC635\uC158: ${T(n.values, "\uB610\uB294 ")} \uC911 \uD558\uB098\uC5EC\uC57C \uD569\uB2C8\uB2E4`;
      case "too_big": {
        let i = n.inclusive ? "\uC774\uD558" : "\uBBF8\uB9CC", s = i === "\uBBF8\uB9CC" ? "\uC774\uC5B4\uC57C \uD569\uB2C8\uB2E4" : "\uC5EC\uC57C \uD569\uB2C8\uB2E4", a = t(n.origin), c = a?.unit ?? "\uC694\uC18C";
        if (a) return `${n.origin ?? "\uAC12"}\uC774 \uB108\uBB34 \uD07D\uB2C8\uB2E4: ${n.maximum.toString()}${c} ${i}${s}`;
        return `${n.origin ?? "\uAC12"}\uC774 \uB108\uBB34 \uD07D\uB2C8\uB2E4: ${n.maximum.toString()} ${i}${s}`;
      }
      case "too_small": {
        let i = n.inclusive ? "\uC774\uC0C1" : "\uCD08\uACFC", s = i === "\uC774\uC0C1" ? "\uC774\uC5B4\uC57C \uD569\uB2C8\uB2E4" : "\uC5EC\uC57C \uD569\uB2C8\uB2E4", a = t(n.origin), c = a?.unit ?? "\uC694\uC18C";
        if (a) return `${n.origin ?? "\uAC12"}\uC774 \uB108\uBB34 \uC791\uC2B5\uB2C8\uB2E4: ${n.minimum.toString()}${c} ${i}${s}`;
        return `${n.origin ?? "\uAC12"}\uC774 \uB108\uBB34 \uC791\uC2B5\uB2C8\uB2E4: ${n.minimum.toString()} ${i}${s}`;
      }
      case "invalid_format": {
        let i = n;
        if (i.format === "starts_with") return `\uC798\uBABB\uB41C \uBB38\uC790\uC5F4: "${i.prefix}"(\uC73C)\uB85C \uC2DC\uC791\uD574\uC57C \uD569\uB2C8\uB2E4`;
        if (i.format === "ends_with") return `\uC798\uBABB\uB41C \uBB38\uC790\uC5F4: "${i.suffix}"(\uC73C)\uB85C \uB05D\uB098\uC57C \uD569\uB2C8\uB2E4`;
        if (i.format === "includes") return `\uC798\uBABB\uB41C \uBB38\uC790\uC5F4: "${i.includes}"\uC744(\uB97C) \uD3EC\uD568\uD574\uC57C \uD569\uB2C8\uB2E4`;
        if (i.format === "regex") return `\uC798\uBABB\uB41C \uBB38\uC790\uC5F4: \uC815\uADDC\uC2DD ${i.pattern} \uD328\uD134\uACFC \uC77C\uCE58\uD574\uC57C \uD569\uB2C8\uB2E4`;
        return `\uC798\uBABB\uB41C ${o[i.format] ?? n.format}`;
      }
      case "not_multiple_of":
        return `\uC798\uBABB\uB41C \uC22B\uC790: ${n.divisor}\uC758 \uBC30\uC218\uC5EC\uC57C \uD569\uB2C8\uB2E4`;
      case "unrecognized_keys":
        return `\uC778\uC2DD\uD560 \uC218 \uC5C6\uB294 \uD0A4: ${T(n.keys, ", ")}`;
      case "invalid_key":
        return `\uC798\uBABB\uB41C \uD0A4: ${n.origin}`;
      case "invalid_union":
        return "\uC798\uBABB\uB41C \uC785\uB825";
      case "invalid_element":
        return `\uC798\uBABB\uB41C \uAC12: ${n.origin}`;
      default:
        return "\uC798\uBABB\uB41C \uC785\uB825";
    }
  };
};
function T_() {
  return { localeError: kV() };
}
var EV = () => {
  let e = { string: { unit: "\u0437\u043D\u0430\u0446\u0438", verb: "\u0434\u0430 \u0438\u043C\u0430\u0430\u0442" }, file: { unit: "\u0431\u0430\u0458\u0442\u0438", verb: "\u0434\u0430 \u0438\u043C\u0430\u0430\u0442" }, array: { unit: "\u0441\u0442\u0430\u0432\u043A\u0438", verb: "\u0434\u0430 \u0438\u043C\u0430\u0430\u0442" }, set: { unit: "\u0441\u0442\u0430\u0432\u043A\u0438", verb: "\u0434\u0430 \u0438\u043C\u0430\u0430\u0442" } };
  function t(n) {
    return e[n] ?? null;
  }
  let r = (n) => {
    let i = typeof n;
    switch (i) {
      case "number":
        return Number.isNaN(n) ? "NaN" : "\u0431\u0440\u043E\u0458";
      case "object": {
        if (Array.isArray(n)) return "\u043D\u0438\u0437\u0430";
        if (n === null) return "null";
        if (Object.getPrototypeOf(n) !== Object.prototype && n.constructor) return n.constructor.name;
      }
    }
    return i;
  }, o = { regex: "\u0432\u043D\u0435\u0441", email: "\u0430\u0434\u0440\u0435\u0441\u0430 \u043D\u0430 \u0435-\u043F\u043E\u0448\u0442\u0430", url: "URL", emoji: "\u0435\u043C\u043E\u045F\u0438", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "ISO \u0434\u0430\u0442\u0443\u043C \u0438 \u0432\u0440\u0435\u043C\u0435", date: "ISO \u0434\u0430\u0442\u0443\u043C", time: "ISO \u0432\u0440\u0435\u043C\u0435", duration: "ISO \u0432\u0440\u0435\u043C\u0435\u0442\u0440\u0430\u0435\u045A\u0435", ipv4: "IPv4 \u0430\u0434\u0440\u0435\u0441\u0430", ipv6: "IPv6 \u0430\u0434\u0440\u0435\u0441\u0430", cidrv4: "IPv4 \u043E\u043F\u0441\u0435\u0433", cidrv6: "IPv6 \u043E\u043F\u0441\u0435\u0433", base64: "base64-\u0435\u043D\u043A\u043E\u0434\u0438\u0440\u0430\u043D\u0430 \u043D\u0438\u0437\u0430", base64url: "base64url-\u0435\u043D\u043A\u043E\u0434\u0438\u0440\u0430\u043D\u0430 \u043D\u0438\u0437\u0430", json_string: "JSON \u043D\u0438\u0437\u0430", e164: "E.164 \u0431\u0440\u043E\u0458", jwt: "JWT", template_literal: "\u0432\u043D\u0435\u0441" };
  return (n) => {
    switch (n.code) {
      case "invalid_type":
        return `\u0413\u0440\u0435\u0448\u0435\u043D \u0432\u043D\u0435\u0441: \u0441\u0435 \u043E\u0447\u0435\u043A\u0443\u0432\u0430 ${n.expected}, \u043F\u0440\u0438\u043C\u0435\u043D\u043E ${r(n.input)}`;
      case "invalid_value":
        if (n.values.length === 1) return `Invalid input: expected ${D(n.values[0])}`;
        return `\u0413\u0440\u0435\u0448\u0430\u043D\u0430 \u043E\u043F\u0446\u0438\u0458\u0430: \u0441\u0435 \u043E\u0447\u0435\u043A\u0443\u0432\u0430 \u0435\u0434\u043D\u0430 ${T(n.values, "|")}`;
      case "too_big": {
        let i = n.inclusive ? "<=" : "<", s = t(n.origin);
        if (s) return `\u041F\u0440\u0435\u043C\u043D\u043E\u0433\u0443 \u0433\u043E\u043B\u0435\u043C: \u0441\u0435 \u043E\u0447\u0435\u043A\u0443\u0432\u0430 ${n.origin ?? "\u0432\u0440\u0435\u0434\u043D\u043E\u0441\u0442\u0430"} \u0434\u0430 \u0438\u043C\u0430 ${i}${n.maximum.toString()} ${s.unit ?? "\u0435\u043B\u0435\u043C\u0435\u043D\u0442\u0438"}`;
        return `\u041F\u0440\u0435\u043C\u043D\u043E\u0433\u0443 \u0433\u043E\u043B\u0435\u043C: \u0441\u0435 \u043E\u0447\u0435\u043A\u0443\u0432\u0430 ${n.origin ?? "\u0432\u0440\u0435\u0434\u043D\u043E\u0441\u0442\u0430"} \u0434\u0430 \u0431\u0438\u0434\u0435 ${i}${n.maximum.toString()}`;
      }
      case "too_small": {
        let i = n.inclusive ? ">=" : ">", s = t(n.origin);
        if (s) return `\u041F\u0440\u0435\u043C\u043D\u043E\u0433\u0443 \u043C\u0430\u043B: \u0441\u0435 \u043E\u0447\u0435\u043A\u0443\u0432\u0430 ${n.origin} \u0434\u0430 \u0438\u043C\u0430 ${i}${n.minimum.toString()} ${s.unit}`;
        return `\u041F\u0440\u0435\u043C\u043D\u043E\u0433\u0443 \u043C\u0430\u043B: \u0441\u0435 \u043E\u0447\u0435\u043A\u0443\u0432\u0430 ${n.origin} \u0434\u0430 \u0431\u0438\u0434\u0435 ${i}${n.minimum.toString()}`;
      }
      case "invalid_format": {
        let i = n;
        if (i.format === "starts_with") return `\u041D\u0435\u0432\u0430\u0436\u0435\u0447\u043A\u0430 \u043D\u0438\u0437\u0430: \u043C\u043E\u0440\u0430 \u0434\u0430 \u0437\u0430\u043F\u043E\u0447\u043D\u0443\u0432\u0430 \u0441\u043E "${i.prefix}"`;
        if (i.format === "ends_with") return `\u041D\u0435\u0432\u0430\u0436\u0435\u0447\u043A\u0430 \u043D\u0438\u0437\u0430: \u043C\u043E\u0440\u0430 \u0434\u0430 \u0437\u0430\u0432\u0440\u0448\u0443\u0432\u0430 \u0441\u043E "${i.suffix}"`;
        if (i.format === "includes") return `\u041D\u0435\u0432\u0430\u0436\u0435\u0447\u043A\u0430 \u043D\u0438\u0437\u0430: \u043C\u043E\u0440\u0430 \u0434\u0430 \u0432\u043A\u043B\u0443\u0447\u0443\u0432\u0430 "${i.includes}"`;
        if (i.format === "regex") return `\u041D\u0435\u0432\u0430\u0436\u0435\u0447\u043A\u0430 \u043D\u0438\u0437\u0430: \u043C\u043E\u0440\u0430 \u0434\u0430 \u043E\u0434\u0433\u043E\u0430\u0440\u0430 \u043D\u0430 \u043F\u0430\u0442\u0435\u0440\u043D\u043E\u0442 ${i.pattern}`;
        return `Invalid ${o[i.format] ?? n.format}`;
      }
      case "not_multiple_of":
        return `\u0413\u0440\u0435\u0448\u0435\u043D \u0431\u0440\u043E\u0458: \u043C\u043E\u0440\u0430 \u0434\u0430 \u0431\u0438\u0434\u0435 \u0434\u0435\u043B\u0438\u0432 \u0441\u043E ${n.divisor}`;
      case "unrecognized_keys":
        return `${n.keys.length > 1 ? "\u041D\u0435\u043F\u0440\u0435\u043F\u043E\u0437\u043D\u0430\u0435\u043D\u0438 \u043A\u043B\u0443\u0447\u0435\u0432\u0438" : "\u041D\u0435\u043F\u0440\u0435\u043F\u043E\u0437\u043D\u0430\u0435\u043D \u043A\u043B\u0443\u0447"}: ${T(n.keys, ", ")}`;
      case "invalid_key":
        return `\u0413\u0440\u0435\u0448\u0435\u043D \u043A\u043B\u0443\u0447 \u0432\u043E ${n.origin}`;
      case "invalid_union":
        return "\u0413\u0440\u0435\u0448\u0435\u043D \u0432\u043D\u0435\u0441";
      case "invalid_element":
        return `\u0413\u0440\u0435\u0448\u043D\u0430 \u0432\u0440\u0435\u0434\u043D\u043E\u0441\u0442 \u0432\u043E ${n.origin}`;
      default:
        return "\u0413\u0440\u0435\u0448\u0435\u043D \u0432\u043D\u0435\u0441";
    }
  };
};
function P_() {
  return { localeError: EV() };
}
var TV = () => {
  let e = { string: { unit: "aksara", verb: "mempunyai" }, file: { unit: "bait", verb: "mempunyai" }, array: { unit: "elemen", verb: "mempunyai" }, set: { unit: "elemen", verb: "mempunyai" } };
  function t(n) {
    return e[n] ?? null;
  }
  let r = (n) => {
    let i = typeof n;
    switch (i) {
      case "number":
        return Number.isNaN(n) ? "NaN" : "nombor";
      case "object": {
        if (Array.isArray(n)) return "array";
        if (n === null) return "null";
        if (Object.getPrototypeOf(n) !== Object.prototype && n.constructor) return n.constructor.name;
      }
    }
    return i;
  }, o = { regex: "input", email: "alamat e-mel", url: "URL", emoji: "emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "tarikh masa ISO", date: "tarikh ISO", time: "masa ISO", duration: "tempoh ISO", ipv4: "alamat IPv4", ipv6: "alamat IPv6", cidrv4: "julat IPv4", cidrv6: "julat IPv6", base64: "string dikodkan base64", base64url: "string dikodkan base64url", json_string: "string JSON", e164: "nombor E.164", jwt: "JWT", template_literal: "input" };
  return (n) => {
    switch (n.code) {
      case "invalid_type":
        return `Input tidak sah: dijangka ${n.expected}, diterima ${r(n.input)}`;
      case "invalid_value":
        if (n.values.length === 1) return `Input tidak sah: dijangka ${D(n.values[0])}`;
        return `Pilihan tidak sah: dijangka salah satu daripada ${T(n.values, "|")}`;
      case "too_big": {
        let i = n.inclusive ? "<=" : "<", s = t(n.origin);
        if (s) return `Terlalu besar: dijangka ${n.origin ?? "nilai"} ${s.verb} ${i}${n.maximum.toString()} ${s.unit ?? "elemen"}`;
        return `Terlalu besar: dijangka ${n.origin ?? "nilai"} adalah ${i}${n.maximum.toString()}`;
      }
      case "too_small": {
        let i = n.inclusive ? ">=" : ">", s = t(n.origin);
        if (s) return `Terlalu kecil: dijangka ${n.origin} ${s.verb} ${i}${n.minimum.toString()} ${s.unit}`;
        return `Terlalu kecil: dijangka ${n.origin} adalah ${i}${n.minimum.toString()}`;
      }
      case "invalid_format": {
        let i = n;
        if (i.format === "starts_with") return `String tidak sah: mesti bermula dengan "${i.prefix}"`;
        if (i.format === "ends_with") return `String tidak sah: mesti berakhir dengan "${i.suffix}"`;
        if (i.format === "includes") return `String tidak sah: mesti mengandungi "${i.includes}"`;
        if (i.format === "regex") return `String tidak sah: mesti sepadan dengan corak ${i.pattern}`;
        return `${o[i.format] ?? n.format} tidak sah`;
      }
      case "not_multiple_of":
        return `Nombor tidak sah: perlu gandaan ${n.divisor}`;
      case "unrecognized_keys":
        return `Kunci tidak dikenali: ${T(n.keys, ", ")}`;
      case "invalid_key":
        return `Kunci tidak sah dalam ${n.origin}`;
      case "invalid_union":
        return "Input tidak sah";
      case "invalid_element":
        return `Nilai tidak sah dalam ${n.origin}`;
      default:
        return "Input tidak sah";
    }
  };
};
function I_() {
  return { localeError: TV() };
}
var PV = () => {
  let e = { string: { unit: "tekens" }, file: { unit: "bytes" }, array: { unit: "elementen" }, set: { unit: "elementen" } };
  function t(n) {
    return e[n] ?? null;
  }
  let r = (n) => {
    let i = typeof n;
    switch (i) {
      case "number":
        return Number.isNaN(n) ? "NaN" : "getal";
      case "object": {
        if (Array.isArray(n)) return "array";
        if (n === null) return "null";
        if (Object.getPrototypeOf(n) !== Object.prototype && n.constructor) return n.constructor.name;
      }
    }
    return i;
  }, o = { regex: "invoer", email: "emailadres", url: "URL", emoji: "emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "ISO datum en tijd", date: "ISO datum", time: "ISO tijd", duration: "ISO duur", ipv4: "IPv4-adres", ipv6: "IPv6-adres", cidrv4: "IPv4-bereik", cidrv6: "IPv6-bereik", base64: "base64-gecodeerde tekst", base64url: "base64 URL-gecodeerde tekst", json_string: "JSON string", e164: "E.164-nummer", jwt: "JWT", template_literal: "invoer" };
  return (n) => {
    switch (n.code) {
      case "invalid_type":
        return `Ongeldige invoer: verwacht ${n.expected}, ontving ${r(n.input)}`;
      case "invalid_value":
        if (n.values.length === 1) return `Ongeldige invoer: verwacht ${D(n.values[0])}`;
        return `Ongeldige optie: verwacht \xE9\xE9n van ${T(n.values, "|")}`;
      case "too_big": {
        let i = n.inclusive ? "<=" : "<", s = t(n.origin);
        if (s) return `Te lang: verwacht dat ${n.origin ?? "waarde"} ${i}${n.maximum.toString()} ${s.unit ?? "elementen"} bevat`;
        return `Te lang: verwacht dat ${n.origin ?? "waarde"} ${i}${n.maximum.toString()} is`;
      }
      case "too_small": {
        let i = n.inclusive ? ">=" : ">", s = t(n.origin);
        if (s) return `Te kort: verwacht dat ${n.origin} ${i}${n.minimum.toString()} ${s.unit} bevat`;
        return `Te kort: verwacht dat ${n.origin} ${i}${n.minimum.toString()} is`;
      }
      case "invalid_format": {
        let i = n;
        if (i.format === "starts_with") return `Ongeldige tekst: moet met "${i.prefix}" beginnen`;
        if (i.format === "ends_with") return `Ongeldige tekst: moet op "${i.suffix}" eindigen`;
        if (i.format === "includes") return `Ongeldige tekst: moet "${i.includes}" bevatten`;
        if (i.format === "regex") return `Ongeldige tekst: moet overeenkomen met patroon ${i.pattern}`;
        return `Ongeldig: ${o[i.format] ?? n.format}`;
      }
      case "not_multiple_of":
        return `Ongeldig getal: moet een veelvoud van ${n.divisor} zijn`;
      case "unrecognized_keys":
        return `Onbekende key${n.keys.length > 1 ? "s" : ""}: ${T(n.keys, ", ")}`;
      case "invalid_key":
        return `Ongeldige key in ${n.origin}`;
      case "invalid_union":
        return "Ongeldige invoer";
      case "invalid_element":
        return `Ongeldige waarde in ${n.origin}`;
      default:
        return "Ongeldige invoer";
    }
  };
};
function R_() {
  return { localeError: PV() };
}
var IV = () => {
  let e = { string: { unit: "tegn", verb: "\xE5 ha" }, file: { unit: "bytes", verb: "\xE5 ha" }, array: { unit: "elementer", verb: "\xE5 inneholde" }, set: { unit: "elementer", verb: "\xE5 inneholde" } };
  function t(n) {
    return e[n] ?? null;
  }
  let r = (n) => {
    let i = typeof n;
    switch (i) {
      case "number":
        return Number.isNaN(n) ? "NaN" : "tall";
      case "object": {
        if (Array.isArray(n)) return "liste";
        if (n === null) return "null";
        if (Object.getPrototypeOf(n) !== Object.prototype && n.constructor) return n.constructor.name;
      }
    }
    return i;
  }, o = { regex: "input", email: "e-postadresse", url: "URL", emoji: "emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "ISO dato- og klokkeslett", date: "ISO-dato", time: "ISO-klokkeslett", duration: "ISO-varighet", ipv4: "IPv4-omr\xE5de", ipv6: "IPv6-omr\xE5de", cidrv4: "IPv4-spekter", cidrv6: "IPv6-spekter", base64: "base64-enkodet streng", base64url: "base64url-enkodet streng", json_string: "JSON-streng", e164: "E.164-nummer", jwt: "JWT", template_literal: "input" };
  return (n) => {
    switch (n.code) {
      case "invalid_type":
        return `Ugyldig input: forventet ${n.expected}, fikk ${r(n.input)}`;
      case "invalid_value":
        if (n.values.length === 1) return `Ugyldig verdi: forventet ${D(n.values[0])}`;
        return `Ugyldig valg: forventet en av ${T(n.values, "|")}`;
      case "too_big": {
        let i = n.inclusive ? "<=" : "<", s = t(n.origin);
        if (s) return `For stor(t): forventet ${n.origin ?? "value"} til \xE5 ha ${i}${n.maximum.toString()} ${s.unit ?? "elementer"}`;
        return `For stor(t): forventet ${n.origin ?? "value"} til \xE5 ha ${i}${n.maximum.toString()}`;
      }
      case "too_small": {
        let i = n.inclusive ? ">=" : ">", s = t(n.origin);
        if (s) return `For lite(n): forventet ${n.origin} til \xE5 ha ${i}${n.minimum.toString()} ${s.unit}`;
        return `For lite(n): forventet ${n.origin} til \xE5 ha ${i}${n.minimum.toString()}`;
      }
      case "invalid_format": {
        let i = n;
        if (i.format === "starts_with") return `Ugyldig streng: m\xE5 starte med "${i.prefix}"`;
        if (i.format === "ends_with") return `Ugyldig streng: m\xE5 ende med "${i.suffix}"`;
        if (i.format === "includes") return `Ugyldig streng: m\xE5 inneholde "${i.includes}"`;
        if (i.format === "regex") return `Ugyldig streng: m\xE5 matche m\xF8nsteret ${i.pattern}`;
        return `Ugyldig ${o[i.format] ?? n.format}`;
      }
      case "not_multiple_of":
        return `Ugyldig tall: m\xE5 v\xE6re et multiplum av ${n.divisor}`;
      case "unrecognized_keys":
        return `${n.keys.length > 1 ? "Ukjente n\xF8kler" : "Ukjent n\xF8kkel"}: ${T(n.keys, ", ")}`;
      case "invalid_key":
        return `Ugyldig n\xF8kkel i ${n.origin}`;
      case "invalid_union":
        return "Ugyldig input";
      case "invalid_element":
        return `Ugyldig verdi i ${n.origin}`;
      default:
        return "Ugyldig input";
    }
  };
};
function $_() {
  return { localeError: IV() };
}
var RV = () => {
  let e = { string: { unit: "harf", verb: "olmal\u0131d\u0131r" }, file: { unit: "bayt", verb: "olmal\u0131d\u0131r" }, array: { unit: "unsur", verb: "olmal\u0131d\u0131r" }, set: { unit: "unsur", verb: "olmal\u0131d\u0131r" } };
  function t(n) {
    return e[n] ?? null;
  }
  let r = (n) => {
    let i = typeof n;
    switch (i) {
      case "number":
        return Number.isNaN(n) ? "NaN" : "numara";
      case "object": {
        if (Array.isArray(n)) return "saf";
        if (n === null) return "gayb";
        if (Object.getPrototypeOf(n) !== Object.prototype && n.constructor) return n.constructor.name;
      }
    }
    return i;
  }, o = { regex: "giren", email: "epostag\xE2h", url: "URL", emoji: "emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "ISO heng\xE2m\u0131", date: "ISO tarihi", time: "ISO zaman\u0131", duration: "ISO m\xFCddeti", ipv4: "IPv4 ni\u015F\xE2n\u0131", ipv6: "IPv6 ni\u015F\xE2n\u0131", cidrv4: "IPv4 menzili", cidrv6: "IPv6 menzili", base64: "base64-\u015Fifreli metin", base64url: "base64url-\u015Fifreli metin", json_string: "JSON metin", e164: "E.164 say\u0131s\u0131", jwt: "JWT", template_literal: "giren" };
  return (n) => {
    switch (n.code) {
      case "invalid_type":
        return `F\xE2sit giren: umulan ${n.expected}, al\u0131nan ${r(n.input)}`;
      case "invalid_value":
        if (n.values.length === 1) return `F\xE2sit giren: umulan ${D(n.values[0])}`;
        return `F\xE2sit tercih: m\xFBteberler ${T(n.values, "|")}`;
      case "too_big": {
        let i = n.inclusive ? "<=" : "<", s = t(n.origin);
        if (s) return `Fazla b\xFCy\xFCk: ${n.origin ?? "value"}, ${i}${n.maximum.toString()} ${s.unit ?? "elements"} sahip olmal\u0131yd\u0131.`;
        return `Fazla b\xFCy\xFCk: ${n.origin ?? "value"}, ${i}${n.maximum.toString()} olmal\u0131yd\u0131.`;
      }
      case "too_small": {
        let i = n.inclusive ? ">=" : ">", s = t(n.origin);
        if (s) return `Fazla k\xFC\xE7\xFCk: ${n.origin}, ${i}${n.minimum.toString()} ${s.unit} sahip olmal\u0131yd\u0131.`;
        return `Fazla k\xFC\xE7\xFCk: ${n.origin}, ${i}${n.minimum.toString()} olmal\u0131yd\u0131.`;
      }
      case "invalid_format": {
        let i = n;
        if (i.format === "starts_with") return `F\xE2sit metin: "${i.prefix}" ile ba\u015Flamal\u0131.`;
        if (i.format === "ends_with") return `F\xE2sit metin: "${i.suffix}" ile bitmeli.`;
        if (i.format === "includes") return `F\xE2sit metin: "${i.includes}" ihtiv\xE2 etmeli.`;
        if (i.format === "regex") return `F\xE2sit metin: ${i.pattern} nak\u015F\u0131na uymal\u0131.`;
        return `F\xE2sit ${o[i.format] ?? n.format}`;
      }
      case "not_multiple_of":
        return `F\xE2sit say\u0131: ${n.divisor} kat\u0131 olmal\u0131yd\u0131.`;
      case "unrecognized_keys":
        return `Tan\u0131nmayan anahtar ${n.keys.length > 1 ? "s" : ""}: ${T(n.keys, ", ")}`;
      case "invalid_key":
        return `${n.origin} i\xE7in tan\u0131nmayan anahtar var.`;
      case "invalid_union":
        return "Giren tan\u0131namad\u0131.";
      case "invalid_element":
        return `${n.origin} i\xE7in tan\u0131nmayan k\u0131ymet var.`;
      default:
        return "K\u0131ymet tan\u0131namad\u0131.";
    }
  };
};
function O_() {
  return { localeError: RV() };
}
var $V = () => {
  let e = { string: { unit: "\u062A\u0648\u06A9\u064A", verb: "\u0648\u0644\u0631\u064A" }, file: { unit: "\u0628\u0627\u06CC\u067C\u0633", verb: "\u0648\u0644\u0631\u064A" }, array: { unit: "\u062A\u0648\u06A9\u064A", verb: "\u0648\u0644\u0631\u064A" }, set: { unit: "\u062A\u0648\u06A9\u064A", verb: "\u0648\u0644\u0631\u064A" } };
  function t(n) {
    return e[n] ?? null;
  }
  let r = (n) => {
    let i = typeof n;
    switch (i) {
      case "number":
        return Number.isNaN(n) ? "NaN" : "\u0639\u062F\u062F";
      case "object": {
        if (Array.isArray(n)) return "\u0627\u0631\u06D0";
        if (n === null) return "null";
        if (Object.getPrototypeOf(n) !== Object.prototype && n.constructor) return n.constructor.name;
      }
    }
    return i;
  }, o = { regex: "\u0648\u0631\u0648\u062F\u064A", email: "\u0628\u0631\u06CC\u069A\u0646\u0627\u0644\u06CC\u06A9", url: "\u06CC\u0648 \u0622\u0631 \u0627\u0644", emoji: "\u0627\u06CC\u0645\u0648\u062C\u064A", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "\u0646\u06CC\u067C\u0647 \u0627\u0648 \u0648\u062E\u062A", date: "\u0646\u06D0\u067C\u0647", time: "\u0648\u062E\u062A", duration: "\u0645\u0648\u062F\u0647", ipv4: "\u062F IPv4 \u067E\u062A\u0647", ipv6: "\u062F IPv6 \u067E\u062A\u0647", cidrv4: "\u062F IPv4 \u0633\u0627\u062D\u0647", cidrv6: "\u062F IPv6 \u0633\u0627\u062D\u0647", base64: "base64-encoded \u0645\u062A\u0646", base64url: "base64url-encoded \u0645\u062A\u0646", json_string: "JSON \u0645\u062A\u0646", e164: "\u062F E.164 \u0634\u0645\u06D0\u0631\u0647", jwt: "JWT", template_literal: "\u0648\u0631\u0648\u062F\u064A" };
  return (n) => {
    switch (n.code) {
      case "invalid_type":
        return `\u0646\u0627\u0633\u0645 \u0648\u0631\u0648\u062F\u064A: \u0628\u0627\u06CC\u062F ${n.expected} \u0648\u0627\u06CC, \u0645\u06AB\u0631 ${r(n.input)} \u062A\u0631\u0644\u0627\u0633\u0647 \u0634\u0648`;
      case "invalid_value":
        if (n.values.length === 1) return `\u0646\u0627\u0633\u0645 \u0648\u0631\u0648\u062F\u064A: \u0628\u0627\u06CC\u062F ${D(n.values[0])} \u0648\u0627\u06CC`;
        return `\u0646\u0627\u0633\u0645 \u0627\u0646\u062A\u062E\u0627\u0628: \u0628\u0627\u06CC\u062F \u06CC\u0648 \u0644\u0647 ${T(n.values, "|")} \u0685\u062E\u0647 \u0648\u0627\u06CC`;
      case "too_big": {
        let i = n.inclusive ? "<=" : "<", s = t(n.origin);
        if (s) return `\u0689\u06CC\u0631 \u0644\u0648\u06CC: ${n.origin ?? "\u0627\u0631\u0632\u069A\u062A"} \u0628\u0627\u06CC\u062F ${i}${n.maximum.toString()} ${s.unit ?? "\u0639\u0646\u0635\u0631\u0648\u0646\u0647"} \u0648\u0644\u0631\u064A`;
        return `\u0689\u06CC\u0631 \u0644\u0648\u06CC: ${n.origin ?? "\u0627\u0631\u0632\u069A\u062A"} \u0628\u0627\u06CC\u062F ${i}${n.maximum.toString()} \u0648\u064A`;
      }
      case "too_small": {
        let i = n.inclusive ? ">=" : ">", s = t(n.origin);
        if (s) return `\u0689\u06CC\u0631 \u06A9\u0648\u0686\u0646\u06CC: ${n.origin} \u0628\u0627\u06CC\u062F ${i}${n.minimum.toString()} ${s.unit} \u0648\u0644\u0631\u064A`;
        return `\u0689\u06CC\u0631 \u06A9\u0648\u0686\u0646\u06CC: ${n.origin} \u0628\u0627\u06CC\u062F ${i}${n.minimum.toString()} \u0648\u064A`;
      }
      case "invalid_format": {
        let i = n;
        if (i.format === "starts_with") return `\u0646\u0627\u0633\u0645 \u0645\u062A\u0646: \u0628\u0627\u06CC\u062F \u062F "${i.prefix}" \u0633\u0631\u0647 \u067E\u06CC\u0644 \u0634\u064A`;
        if (i.format === "ends_with") return `\u0646\u0627\u0633\u0645 \u0645\u062A\u0646: \u0628\u0627\u06CC\u062F \u062F "${i.suffix}" \u0633\u0631\u0647 \u067E\u0627\u06CC \u062A\u0647 \u0648\u0631\u0633\u064A\u0696\u064A`;
        if (i.format === "includes") return `\u0646\u0627\u0633\u0645 \u0645\u062A\u0646: \u0628\u0627\u06CC\u062F "${i.includes}" \u0648\u0644\u0631\u064A`;
        if (i.format === "regex") return `\u0646\u0627\u0633\u0645 \u0645\u062A\u0646: \u0628\u0627\u06CC\u062F \u062F ${i.pattern} \u0633\u0631\u0647 \u0645\u0637\u0627\u0628\u0642\u062A \u0648\u0644\u0631\u064A`;
        return `${o[i.format] ?? n.format} \u0646\u0627\u0633\u0645 \u062F\u06CC`;
      }
      case "not_multiple_of":
        return `\u0646\u0627\u0633\u0645 \u0639\u062F\u062F: \u0628\u0627\u06CC\u062F \u062F ${n.divisor} \u0645\u0636\u0631\u0628 \u0648\u064A`;
      case "unrecognized_keys":
        return `\u0646\u0627\u0633\u0645 ${n.keys.length > 1 ? "\u06A9\u0644\u06CC\u0689\u0648\u0646\u0647" : "\u06A9\u0644\u06CC\u0689"}: ${T(n.keys, ", ")}`;
      case "invalid_key":
        return `\u0646\u0627\u0633\u0645 \u06A9\u0644\u06CC\u0689 \u067E\u0647 ${n.origin} \u06A9\u06D0`;
      case "invalid_union":
        return "\u0646\u0627\u0633\u0645\u0647 \u0648\u0631\u0648\u062F\u064A";
      case "invalid_element":
        return `\u0646\u0627\u0633\u0645 \u0639\u0646\u0635\u0631 \u067E\u0647 ${n.origin} \u06A9\u06D0`;
      default:
        return "\u0646\u0627\u0633\u0645\u0647 \u0648\u0631\u0648\u062F\u064A";
    }
  };
};
function A_() {
  return { localeError: $V() };
}
var OV = () => {
  let e = { string: { unit: "znak\xF3w", verb: "mie\u0107" }, file: { unit: "bajt\xF3w", verb: "mie\u0107" }, array: { unit: "element\xF3w", verb: "mie\u0107" }, set: { unit: "element\xF3w", verb: "mie\u0107" } };
  function t(n) {
    return e[n] ?? null;
  }
  let r = (n) => {
    let i = typeof n;
    switch (i) {
      case "number":
        return Number.isNaN(n) ? "NaN" : "liczba";
      case "object": {
        if (Array.isArray(n)) return "tablica";
        if (n === null) return "null";
        if (Object.getPrototypeOf(n) !== Object.prototype && n.constructor) return n.constructor.name;
      }
    }
    return i;
  }, o = { regex: "wyra\u017Cenie", email: "adres email", url: "URL", emoji: "emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "data i godzina w formacie ISO", date: "data w formacie ISO", time: "godzina w formacie ISO", duration: "czas trwania ISO", ipv4: "adres IPv4", ipv6: "adres IPv6", cidrv4: "zakres IPv4", cidrv6: "zakres IPv6", base64: "ci\u0105g znak\xF3w zakodowany w formacie base64", base64url: "ci\u0105g znak\xF3w zakodowany w formacie base64url", json_string: "ci\u0105g znak\xF3w w formacie JSON", e164: "liczba E.164", jwt: "JWT", template_literal: "wej\u015Bcie" };
  return (n) => {
    switch (n.code) {
      case "invalid_type":
        return `Nieprawid\u0142owe dane wej\u015Bciowe: oczekiwano ${n.expected}, otrzymano ${r(n.input)}`;
      case "invalid_value":
        if (n.values.length === 1) return `Nieprawid\u0142owe dane wej\u015Bciowe: oczekiwano ${D(n.values[0])}`;
        return `Nieprawid\u0142owa opcja: oczekiwano jednej z warto\u015Bci ${T(n.values, "|")}`;
      case "too_big": {
        let i = n.inclusive ? "<=" : "<", s = t(n.origin);
        if (s) return `Za du\u017Ca warto\u015B\u0107: oczekiwano, \u017Ce ${n.origin ?? "warto\u015B\u0107"} b\u0119dzie mie\u0107 ${i}${n.maximum.toString()} ${s.unit ?? "element\xF3w"}`;
        return `Zbyt du\u017C(y/a/e): oczekiwano, \u017Ce ${n.origin ?? "warto\u015B\u0107"} b\u0119dzie wynosi\u0107 ${i}${n.maximum.toString()}`;
      }
      case "too_small": {
        let i = n.inclusive ? ">=" : ">", s = t(n.origin);
        if (s) return `Za ma\u0142a warto\u015B\u0107: oczekiwano, \u017Ce ${n.origin ?? "warto\u015B\u0107"} b\u0119dzie mie\u0107 ${i}${n.minimum.toString()} ${s.unit ?? "element\xF3w"}`;
        return `Zbyt ma\u0142(y/a/e): oczekiwano, \u017Ce ${n.origin ?? "warto\u015B\u0107"} b\u0119dzie wynosi\u0107 ${i}${n.minimum.toString()}`;
      }
      case "invalid_format": {
        let i = n;
        if (i.format === "starts_with") return `Nieprawid\u0142owy ci\u0105g znak\xF3w: musi zaczyna\u0107 si\u0119 od "${i.prefix}"`;
        if (i.format === "ends_with") return `Nieprawid\u0142owy ci\u0105g znak\xF3w: musi ko\u0144czy\u0107 si\u0119 na "${i.suffix}"`;
        if (i.format === "includes") return `Nieprawid\u0142owy ci\u0105g znak\xF3w: musi zawiera\u0107 "${i.includes}"`;
        if (i.format === "regex") return `Nieprawid\u0142owy ci\u0105g znak\xF3w: musi odpowiada\u0107 wzorcowi ${i.pattern}`;
        return `Nieprawid\u0142ow(y/a/e) ${o[i.format] ?? n.format}`;
      }
      case "not_multiple_of":
        return `Nieprawid\u0142owa liczba: musi by\u0107 wielokrotno\u015Bci\u0105 ${n.divisor}`;
      case "unrecognized_keys":
        return `Nierozpoznane klucze${n.keys.length > 1 ? "s" : ""}: ${T(n.keys, ", ")}`;
      case "invalid_key":
        return `Nieprawid\u0142owy klucz w ${n.origin}`;
      case "invalid_union":
        return "Nieprawid\u0142owe dane wej\u015Bciowe";
      case "invalid_element":
        return `Nieprawid\u0142owa warto\u015B\u0107 w ${n.origin}`;
      default:
        return "Nieprawid\u0142owe dane wej\u015Bciowe";
    }
  };
};
function C_() {
  return { localeError: OV() };
}
var AV = () => {
  let e = { string: { unit: "caracteres", verb: "ter" }, file: { unit: "bytes", verb: "ter" }, array: { unit: "itens", verb: "ter" }, set: { unit: "itens", verb: "ter" } };
  function t(n) {
    return e[n] ?? null;
  }
  let r = (n) => {
    let i = typeof n;
    switch (i) {
      case "number":
        return Number.isNaN(n) ? "NaN" : "n\xFAmero";
      case "object": {
        if (Array.isArray(n)) return "array";
        if (n === null) return "nulo";
        if (Object.getPrototypeOf(n) !== Object.prototype && n.constructor) return n.constructor.name;
      }
    }
    return i;
  }, o = { regex: "padr\xE3o", email: "endere\xE7o de e-mail", url: "URL", emoji: "emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "data e hora ISO", date: "data ISO", time: "hora ISO", duration: "dura\xE7\xE3o ISO", ipv4: "endere\xE7o IPv4", ipv6: "endere\xE7o IPv6", cidrv4: "faixa de IPv4", cidrv6: "faixa de IPv6", base64: "texto codificado em base64", base64url: "URL codificada em base64", json_string: "texto JSON", e164: "n\xFAmero E.164", jwt: "JWT", template_literal: "entrada" };
  return (n) => {
    switch (n.code) {
      case "invalid_type":
        return `Tipo inv\xE1lido: esperado ${n.expected}, recebido ${r(n.input)}`;
      case "invalid_value":
        if (n.values.length === 1) return `Entrada inv\xE1lida: esperado ${D(n.values[0])}`;
        return `Op\xE7\xE3o inv\xE1lida: esperada uma das ${T(n.values, "|")}`;
      case "too_big": {
        let i = n.inclusive ? "<=" : "<", s = t(n.origin);
        if (s) return `Muito grande: esperado que ${n.origin ?? "valor"} tivesse ${i}${n.maximum.toString()} ${s.unit ?? "elementos"}`;
        return `Muito grande: esperado que ${n.origin ?? "valor"} fosse ${i}${n.maximum.toString()}`;
      }
      case "too_small": {
        let i = n.inclusive ? ">=" : ">", s = t(n.origin);
        if (s) return `Muito pequeno: esperado que ${n.origin} tivesse ${i}${n.minimum.toString()} ${s.unit}`;
        return `Muito pequeno: esperado que ${n.origin} fosse ${i}${n.minimum.toString()}`;
      }
      case "invalid_format": {
        let i = n;
        if (i.format === "starts_with") return `Texto inv\xE1lido: deve come\xE7ar com "${i.prefix}"`;
        if (i.format === "ends_with") return `Texto inv\xE1lido: deve terminar com "${i.suffix}"`;
        if (i.format === "includes") return `Texto inv\xE1lido: deve incluir "${i.includes}"`;
        if (i.format === "regex") return `Texto inv\xE1lido: deve corresponder ao padr\xE3o ${i.pattern}`;
        return `${o[i.format] ?? n.format} inv\xE1lido`;
      }
      case "not_multiple_of":
        return `N\xFAmero inv\xE1lido: deve ser m\xFAltiplo de ${n.divisor}`;
      case "unrecognized_keys":
        return `Chave${n.keys.length > 1 ? "s" : ""} desconhecida${n.keys.length > 1 ? "s" : ""}: ${T(n.keys, ", ")}`;
      case "invalid_key":
        return `Chave inv\xE1lida em ${n.origin}`;
      case "invalid_union":
        return "Entrada inv\xE1lida";
      case "invalid_element":
        return `Valor inv\xE1lido em ${n.origin}`;
      default:
        return "Campo inv\xE1lido";
    }
  };
};
function M_() {
  return { localeError: AV() };
}
function X0(e, t, r, o) {
  let n = Math.abs(e), i = n % 10, s = n % 100;
  if (s >= 11 && s <= 19) return o;
  if (i === 1) return t;
  if (i >= 2 && i <= 4) return r;
  return o;
}
var CV = () => {
  let e = { string: { unit: { one: "\u0441\u0438\u043C\u0432\u043E\u043B", few: "\u0441\u0438\u043C\u0432\u043E\u043B\u0430", many: "\u0441\u0438\u043C\u0432\u043E\u043B\u043E\u0432" }, verb: "\u0438\u043C\u0435\u0442\u044C" }, file: { unit: { one: "\u0431\u0430\u0439\u0442", few: "\u0431\u0430\u0439\u0442\u0430", many: "\u0431\u0430\u0439\u0442" }, verb: "\u0438\u043C\u0435\u0442\u044C" }, array: { unit: { one: "\u044D\u043B\u0435\u043C\u0435\u043D\u0442", few: "\u044D\u043B\u0435\u043C\u0435\u043D\u0442\u0430", many: "\u044D\u043B\u0435\u043C\u0435\u043D\u0442\u043E\u0432" }, verb: "\u0438\u043C\u0435\u0442\u044C" }, set: { unit: { one: "\u044D\u043B\u0435\u043C\u0435\u043D\u0442", few: "\u044D\u043B\u0435\u043C\u0435\u043D\u0442\u0430", many: "\u044D\u043B\u0435\u043C\u0435\u043D\u0442\u043E\u0432" }, verb: "\u0438\u043C\u0435\u0442\u044C" } };
  function t(n) {
    return e[n] ?? null;
  }
  let r = (n) => {
    let i = typeof n;
    switch (i) {
      case "number":
        return Number.isNaN(n) ? "NaN" : "\u0447\u0438\u0441\u043B\u043E";
      case "object": {
        if (Array.isArray(n)) return "\u043C\u0430\u0441\u0441\u0438\u0432";
        if (n === null) return "null";
        if (Object.getPrototypeOf(n) !== Object.prototype && n.constructor) return n.constructor.name;
      }
    }
    return i;
  }, o = { regex: "\u0432\u0432\u043E\u0434", email: "email \u0430\u0434\u0440\u0435\u0441", url: "URL", emoji: "\u044D\u043C\u043E\u0434\u0437\u0438", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "ISO \u0434\u0430\u0442\u0430 \u0438 \u0432\u0440\u0435\u043C\u044F", date: "ISO \u0434\u0430\u0442\u0430", time: "ISO \u0432\u0440\u0435\u043C\u044F", duration: "ISO \u0434\u043B\u0438\u0442\u0435\u043B\u044C\u043D\u043E\u0441\u0442\u044C", ipv4: "IPv4 \u0430\u0434\u0440\u0435\u0441", ipv6: "IPv6 \u0430\u0434\u0440\u0435\u0441", cidrv4: "IPv4 \u0434\u0438\u0430\u043F\u0430\u0437\u043E\u043D", cidrv6: "IPv6 \u0434\u0438\u0430\u043F\u0430\u0437\u043E\u043D", base64: "\u0441\u0442\u0440\u043E\u043A\u0430 \u0432 \u0444\u043E\u0440\u043C\u0430\u0442\u0435 base64", base64url: "\u0441\u0442\u0440\u043E\u043A\u0430 \u0432 \u0444\u043E\u0440\u043C\u0430\u0442\u0435 base64url", json_string: "JSON \u0441\u0442\u0440\u043E\u043A\u0430", e164: "\u043D\u043E\u043C\u0435\u0440 E.164", jwt: "JWT", template_literal: "\u0432\u0432\u043E\u0434" };
  return (n) => {
    switch (n.code) {
      case "invalid_type":
        return `\u041D\u0435\u0432\u0435\u0440\u043D\u044B\u0439 \u0432\u0432\u043E\u0434: \u043E\u0436\u0438\u0434\u0430\u043B\u043E\u0441\u044C ${n.expected}, \u043F\u043E\u043B\u0443\u0447\u0435\u043D\u043E ${r(n.input)}`;
      case "invalid_value":
        if (n.values.length === 1) return `\u041D\u0435\u0432\u0435\u0440\u043D\u044B\u0439 \u0432\u0432\u043E\u0434: \u043E\u0436\u0438\u0434\u0430\u043B\u043E\u0441\u044C ${D(n.values[0])}`;
        return `\u041D\u0435\u0432\u0435\u0440\u043D\u044B\u0439 \u0432\u0430\u0440\u0438\u0430\u043D\u0442: \u043E\u0436\u0438\u0434\u0430\u043B\u043E\u0441\u044C \u043E\u0434\u043D\u043E \u0438\u0437 ${T(n.values, "|")}`;
      case "too_big": {
        let i = n.inclusive ? "<=" : "<", s = t(n.origin);
        if (s) {
          let a = Number(n.maximum), c = X0(a, s.unit.one, s.unit.few, s.unit.many);
          return `\u0421\u043B\u0438\u0448\u043A\u043E\u043C \u0431\u043E\u043B\u044C\u0448\u043E\u0435 \u0437\u043D\u0430\u0447\u0435\u043D\u0438\u0435: \u043E\u0436\u0438\u0434\u0430\u043B\u043E\u0441\u044C, \u0447\u0442\u043E ${n.origin ?? "\u0437\u043D\u0430\u0447\u0435\u043D\u0438\u0435"} \u0431\u0443\u0434\u0435\u0442 \u0438\u043C\u0435\u0442\u044C ${i}${n.maximum.toString()} ${c}`;
        }
        return `\u0421\u043B\u0438\u0448\u043A\u043E\u043C \u0431\u043E\u043B\u044C\u0448\u043E\u0435 \u0437\u043D\u0430\u0447\u0435\u043D\u0438\u0435: \u043E\u0436\u0438\u0434\u0430\u043B\u043E\u0441\u044C, \u0447\u0442\u043E ${n.origin ?? "\u0437\u043D\u0430\u0447\u0435\u043D\u0438\u0435"} \u0431\u0443\u0434\u0435\u0442 ${i}${n.maximum.toString()}`;
      }
      case "too_small": {
        let i = n.inclusive ? ">=" : ">", s = t(n.origin);
        if (s) {
          let a = Number(n.minimum), c = X0(a, s.unit.one, s.unit.few, s.unit.many);
          return `\u0421\u043B\u0438\u0448\u043A\u043E\u043C \u043C\u0430\u043B\u0435\u043D\u044C\u043A\u043E\u0435 \u0437\u043D\u0430\u0447\u0435\u043D\u0438\u0435: \u043E\u0436\u0438\u0434\u0430\u043B\u043E\u0441\u044C, \u0447\u0442\u043E ${n.origin} \u0431\u0443\u0434\u0435\u0442 \u0438\u043C\u0435\u0442\u044C ${i}${n.minimum.toString()} ${c}`;
        }
        return `\u0421\u043B\u0438\u0448\u043A\u043E\u043C \u043C\u0430\u043B\u0435\u043D\u044C\u043A\u043E\u0435 \u0437\u043D\u0430\u0447\u0435\u043D\u0438\u0435: \u043E\u0436\u0438\u0434\u0430\u043B\u043E\u0441\u044C, \u0447\u0442\u043E ${n.origin} \u0431\u0443\u0434\u0435\u0442 ${i}${n.minimum.toString()}`;
      }
      case "invalid_format": {
        let i = n;
        if (i.format === "starts_with") return `\u041D\u0435\u0432\u0435\u0440\u043D\u0430\u044F \u0441\u0442\u0440\u043E\u043A\u0430: \u0434\u043E\u043B\u0436\u043D\u0430 \u043D\u0430\u0447\u0438\u043D\u0430\u0442\u044C\u0441\u044F \u0441 "${i.prefix}"`;
        if (i.format === "ends_with") return `\u041D\u0435\u0432\u0435\u0440\u043D\u0430\u044F \u0441\u0442\u0440\u043E\u043A\u0430: \u0434\u043E\u043B\u0436\u043D\u0430 \u0437\u0430\u043A\u0430\u043D\u0447\u0438\u0432\u0430\u0442\u044C\u0441\u044F \u043D\u0430 "${i.suffix}"`;
        if (i.format === "includes") return `\u041D\u0435\u0432\u0435\u0440\u043D\u0430\u044F \u0441\u0442\u0440\u043E\u043A\u0430: \u0434\u043E\u043B\u0436\u043D\u0430 \u0441\u043E\u0434\u0435\u0440\u0436\u0430\u0442\u044C "${i.includes}"`;
        if (i.format === "regex") return `\u041D\u0435\u0432\u0435\u0440\u043D\u0430\u044F \u0441\u0442\u0440\u043E\u043A\u0430: \u0434\u043E\u043B\u0436\u043D\u0430 \u0441\u043E\u043E\u0442\u0432\u0435\u0442\u0441\u0442\u0432\u043E\u0432\u0430\u0442\u044C \u0448\u0430\u0431\u043B\u043E\u043D\u0443 ${i.pattern}`;
        return `\u041D\u0435\u0432\u0435\u0440\u043D\u044B\u0439 ${o[i.format] ?? n.format}`;
      }
      case "not_multiple_of":
        return `\u041D\u0435\u0432\u0435\u0440\u043D\u043E\u0435 \u0447\u0438\u0441\u043B\u043E: \u0434\u043E\u043B\u0436\u043D\u043E \u0431\u044B\u0442\u044C \u043A\u0440\u0430\u0442\u043D\u044B\u043C ${n.divisor}`;
      case "unrecognized_keys":
        return `\u041D\u0435\u0440\u0430\u0441\u043F\u043E\u0437\u043D\u0430\u043D\u043D${n.keys.length > 1 ? "\u044B\u0435" : "\u044B\u0439"} \u043A\u043B\u044E\u0447${n.keys.length > 1 ? "\u0438" : ""}: ${T(n.keys, ", ")}`;
      case "invalid_key":
        return `\u041D\u0435\u0432\u0435\u0440\u043D\u044B\u0439 \u043A\u043B\u044E\u0447 \u0432 ${n.origin}`;
      case "invalid_union":
        return "\u041D\u0435\u0432\u0435\u0440\u043D\u044B\u0435 \u0432\u0445\u043E\u0434\u043D\u044B\u0435 \u0434\u0430\u043D\u043D\u044B\u0435";
      case "invalid_element":
        return `\u041D\u0435\u0432\u0435\u0440\u043D\u043E\u0435 \u0437\u043D\u0430\u0447\u0435\u043D\u0438\u0435 \u0432 ${n.origin}`;
      default:
        return "\u041D\u0435\u0432\u0435\u0440\u043D\u044B\u0435 \u0432\u0445\u043E\u0434\u043D\u044B\u0435 \u0434\u0430\u043D\u043D\u044B\u0435";
    }
  };
};
function D_() {
  return { localeError: CV() };
}
var MV = () => {
  let e = { string: { unit: "znakov", verb: "imeti" }, file: { unit: "bajtov", verb: "imeti" }, array: { unit: "elementov", verb: "imeti" }, set: { unit: "elementov", verb: "imeti" } };
  function t(n) {
    return e[n] ?? null;
  }
  let r = (n) => {
    let i = typeof n;
    switch (i) {
      case "number":
        return Number.isNaN(n) ? "NaN" : "\u0161tevilo";
      case "object": {
        if (Array.isArray(n)) return "tabela";
        if (n === null) return "null";
        if (Object.getPrototypeOf(n) !== Object.prototype && n.constructor) return n.constructor.name;
      }
    }
    return i;
  }, o = { regex: "vnos", email: "e-po\u0161tni naslov", url: "URL", emoji: "emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "ISO datum in \u010Das", date: "ISO datum", time: "ISO \u010Das", duration: "ISO trajanje", ipv4: "IPv4 naslov", ipv6: "IPv6 naslov", cidrv4: "obseg IPv4", cidrv6: "obseg IPv6", base64: "base64 kodiran niz", base64url: "base64url kodiran niz", json_string: "JSON niz", e164: "E.164 \u0161tevilka", jwt: "JWT", template_literal: "vnos" };
  return (n) => {
    switch (n.code) {
      case "invalid_type":
        return `Neveljaven vnos: pri\u010Dakovano ${n.expected}, prejeto ${r(n.input)}`;
      case "invalid_value":
        if (n.values.length === 1) return `Neveljaven vnos: pri\u010Dakovano ${D(n.values[0])}`;
        return `Neveljavna mo\u017Enost: pri\u010Dakovano eno izmed ${T(n.values, "|")}`;
      case "too_big": {
        let i = n.inclusive ? "<=" : "<", s = t(n.origin);
        if (s) return `Preveliko: pri\u010Dakovano, da bo ${n.origin ?? "vrednost"} imelo ${i}${n.maximum.toString()} ${s.unit ?? "elementov"}`;
        return `Preveliko: pri\u010Dakovano, da bo ${n.origin ?? "vrednost"} ${i}${n.maximum.toString()}`;
      }
      case "too_small": {
        let i = n.inclusive ? ">=" : ">", s = t(n.origin);
        if (s) return `Premajhno: pri\u010Dakovano, da bo ${n.origin} imelo ${i}${n.minimum.toString()} ${s.unit}`;
        return `Premajhno: pri\u010Dakovano, da bo ${n.origin} ${i}${n.minimum.toString()}`;
      }
      case "invalid_format": {
        let i = n;
        if (i.format === "starts_with") return `Neveljaven niz: mora se za\u010Deti z "${i.prefix}"`;
        if (i.format === "ends_with") return `Neveljaven niz: mora se kon\u010Dati z "${i.suffix}"`;
        if (i.format === "includes") return `Neveljaven niz: mora vsebovati "${i.includes}"`;
        if (i.format === "regex") return `Neveljaven niz: mora ustrezati vzorcu ${i.pattern}`;
        return `Neveljaven ${o[i.format] ?? n.format}`;
      }
      case "not_multiple_of":
        return `Neveljavno \u0161tevilo: mora biti ve\u010Dkratnik ${n.divisor}`;
      case "unrecognized_keys":
        return `Neprepoznan${n.keys.length > 1 ? "i klju\u010Di" : " klju\u010D"}: ${T(n.keys, ", ")}`;
      case "invalid_key":
        return `Neveljaven klju\u010D v ${n.origin}`;
      case "invalid_union":
        return "Neveljaven vnos";
      case "invalid_element":
        return `Neveljavna vrednost v ${n.origin}`;
      default:
        return "Neveljaven vnos";
    }
  };
};
function N_() {
  return { localeError: MV() };
}
var DV = () => {
  let e = { string: { unit: "tecken", verb: "att ha" }, file: { unit: "bytes", verb: "att ha" }, array: { unit: "objekt", verb: "att inneh\xE5lla" }, set: { unit: "objekt", verb: "att inneh\xE5lla" } };
  function t(n) {
    return e[n] ?? null;
  }
  let r = (n) => {
    let i = typeof n;
    switch (i) {
      case "number":
        return Number.isNaN(n) ? "NaN" : "antal";
      case "object": {
        if (Array.isArray(n)) return "lista";
        if (n === null) return "null";
        if (Object.getPrototypeOf(n) !== Object.prototype && n.constructor) return n.constructor.name;
      }
    }
    return i;
  }, o = { regex: "regulj\xE4rt uttryck", email: "e-postadress", url: "URL", emoji: "emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "ISO-datum och tid", date: "ISO-datum", time: "ISO-tid", duration: "ISO-varaktighet", ipv4: "IPv4-intervall", ipv6: "IPv6-intervall", cidrv4: "IPv4-spektrum", cidrv6: "IPv6-spektrum", base64: "base64-kodad str\xE4ng", base64url: "base64url-kodad str\xE4ng", json_string: "JSON-str\xE4ng", e164: "E.164-nummer", jwt: "JWT", template_literal: "mall-literal" };
  return (n) => {
    switch (n.code) {
      case "invalid_type":
        return `Ogiltig inmatning: f\xF6rv\xE4ntat ${n.expected}, fick ${r(n.input)}`;
      case "invalid_value":
        if (n.values.length === 1) return `Ogiltig inmatning: f\xF6rv\xE4ntat ${D(n.values[0])}`;
        return `Ogiltigt val: f\xF6rv\xE4ntade en av ${T(n.values, "|")}`;
      case "too_big": {
        let i = n.inclusive ? "<=" : "<", s = t(n.origin);
        if (s) return `F\xF6r stor(t): f\xF6rv\xE4ntade ${n.origin ?? "v\xE4rdet"} att ha ${i}${n.maximum.toString()} ${s.unit ?? "element"}`;
        return `F\xF6r stor(t): f\xF6rv\xE4ntat ${n.origin ?? "v\xE4rdet"} att ha ${i}${n.maximum.toString()}`;
      }
      case "too_small": {
        let i = n.inclusive ? ">=" : ">", s = t(n.origin);
        if (s) return `F\xF6r lite(t): f\xF6rv\xE4ntade ${n.origin ?? "v\xE4rdet"} att ha ${i}${n.minimum.toString()} ${s.unit}`;
        return `F\xF6r lite(t): f\xF6rv\xE4ntade ${n.origin ?? "v\xE4rdet"} att ha ${i}${n.minimum.toString()}`;
      }
      case "invalid_format": {
        let i = n;
        if (i.format === "starts_with") return `Ogiltig str\xE4ng: m\xE5ste b\xF6rja med "${i.prefix}"`;
        if (i.format === "ends_with") return `Ogiltig str\xE4ng: m\xE5ste sluta med "${i.suffix}"`;
        if (i.format === "includes") return `Ogiltig str\xE4ng: m\xE5ste inneh\xE5lla "${i.includes}"`;
        if (i.format === "regex") return `Ogiltig str\xE4ng: m\xE5ste matcha m\xF6nstret "${i.pattern}"`;
        return `Ogiltig(t) ${o[i.format] ?? n.format}`;
      }
      case "not_multiple_of":
        return `Ogiltigt tal: m\xE5ste vara en multipel av ${n.divisor}`;
      case "unrecognized_keys":
        return `${n.keys.length > 1 ? "Ok\xE4nda nycklar" : "Ok\xE4nd nyckel"}: ${T(n.keys, ", ")}`;
      case "invalid_key":
        return `Ogiltig nyckel i ${n.origin ?? "v\xE4rdet"}`;
      case "invalid_union":
        return "Ogiltig input";
      case "invalid_element":
        return `Ogiltigt v\xE4rde i ${n.origin ?? "v\xE4rdet"}`;
      default:
        return "Ogiltig input";
    }
  };
};
function j_() {
  return { localeError: DV() };
}
var NV = () => {
  let e = { string: { unit: "\u0B8E\u0BB4\u0BC1\u0BA4\u0BCD\u0BA4\u0BC1\u0B95\u0BCD\u0B95\u0BB3\u0BCD", verb: "\u0B95\u0BCA\u0BA3\u0BCD\u0B9F\u0BBF\u0BB0\u0BC1\u0B95\u0BCD\u0B95 \u0BB5\u0BC7\u0BA3\u0BCD\u0B9F\u0BC1\u0BAE\u0BCD" }, file: { unit: "\u0BAA\u0BC8\u0B9F\u0BCD\u0B9F\u0BC1\u0B95\u0BB3\u0BCD", verb: "\u0B95\u0BCA\u0BA3\u0BCD\u0B9F\u0BBF\u0BB0\u0BC1\u0B95\u0BCD\u0B95 \u0BB5\u0BC7\u0BA3\u0BCD\u0B9F\u0BC1\u0BAE\u0BCD" }, array: { unit: "\u0B89\u0BB1\u0BC1\u0BAA\u0BCD\u0BAA\u0BC1\u0B95\u0BB3\u0BCD", verb: "\u0B95\u0BCA\u0BA3\u0BCD\u0B9F\u0BBF\u0BB0\u0BC1\u0B95\u0BCD\u0B95 \u0BB5\u0BC7\u0BA3\u0BCD\u0B9F\u0BC1\u0BAE\u0BCD" }, set: { unit: "\u0B89\u0BB1\u0BC1\u0BAA\u0BCD\u0BAA\u0BC1\u0B95\u0BB3\u0BCD", verb: "\u0B95\u0BCA\u0BA3\u0BCD\u0B9F\u0BBF\u0BB0\u0BC1\u0B95\u0BCD\u0B95 \u0BB5\u0BC7\u0BA3\u0BCD\u0B9F\u0BC1\u0BAE\u0BCD" } };
  function t(n) {
    return e[n] ?? null;
  }
  let r = (n) => {
    let i = typeof n;
    switch (i) {
      case "number":
        return Number.isNaN(n) ? "\u0B8E\u0BA3\u0BCD \u0B85\u0BB2\u0BCD\u0BB2\u0BBE\u0BA4\u0BA4\u0BC1" : "\u0B8E\u0BA3\u0BCD";
      case "object": {
        if (Array.isArray(n)) return "\u0B85\u0BA3\u0BBF";
        if (n === null) return "\u0BB5\u0BC6\u0BB1\u0BC1\u0BAE\u0BC8";
        if (Object.getPrototypeOf(n) !== Object.prototype && n.constructor) return n.constructor.name;
      }
    }
    return i;
  }, o = { regex: "\u0B89\u0BB3\u0BCD\u0BB3\u0BC0\u0B9F\u0BC1", email: "\u0BAE\u0BBF\u0BA9\u0BCD\u0BA9\u0B9E\u0BCD\u0B9A\u0BB2\u0BCD \u0BAE\u0BC1\u0B95\u0BB5\u0BB0\u0BBF", url: "URL", emoji: "emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "ISO \u0BA4\u0BC7\u0BA4\u0BBF \u0BA8\u0BC7\u0BB0\u0BAE\u0BCD", date: "ISO \u0BA4\u0BC7\u0BA4\u0BBF", time: "ISO \u0BA8\u0BC7\u0BB0\u0BAE\u0BCD", duration: "ISO \u0B95\u0BBE\u0BB2 \u0B85\u0BB3\u0BB5\u0BC1", ipv4: "IPv4 \u0BAE\u0BC1\u0B95\u0BB5\u0BB0\u0BBF", ipv6: "IPv6 \u0BAE\u0BC1\u0B95\u0BB5\u0BB0\u0BBF", cidrv4: "IPv4 \u0BB5\u0BB0\u0BAE\u0BCD\u0BAA\u0BC1", cidrv6: "IPv6 \u0BB5\u0BB0\u0BAE\u0BCD\u0BAA\u0BC1", base64: "base64-encoded \u0B9A\u0BB0\u0BAE\u0BCD", base64url: "base64url-encoded \u0B9A\u0BB0\u0BAE\u0BCD", json_string: "JSON \u0B9A\u0BB0\u0BAE\u0BCD", e164: "E.164 \u0B8E\u0BA3\u0BCD", jwt: "JWT", template_literal: "input" };
  return (n) => {
    switch (n.code) {
      case "invalid_type":
        return `\u0BA4\u0BB5\u0BB1\u0BBE\u0BA9 \u0B89\u0BB3\u0BCD\u0BB3\u0BC0\u0B9F\u0BC1: \u0B8E\u0BA4\u0BBF\u0BB0\u0BCD\u0BAA\u0BBE\u0BB0\u0BCD\u0B95\u0BCD\u0B95\u0BAA\u0BCD\u0BAA\u0B9F\u0BCD\u0B9F\u0BA4\u0BC1 ${n.expected}, \u0BAA\u0BC6\u0BB1\u0BAA\u0BCD\u0BAA\u0B9F\u0BCD\u0B9F\u0BA4\u0BC1 ${r(n.input)}`;
      case "invalid_value":
        if (n.values.length === 1) return `\u0BA4\u0BB5\u0BB1\u0BBE\u0BA9 \u0B89\u0BB3\u0BCD\u0BB3\u0BC0\u0B9F\u0BC1: \u0B8E\u0BA4\u0BBF\u0BB0\u0BCD\u0BAA\u0BBE\u0BB0\u0BCD\u0B95\u0BCD\u0B95\u0BAA\u0BCD\u0BAA\u0B9F\u0BCD\u0B9F\u0BA4\u0BC1 ${D(n.values[0])}`;
        return `\u0BA4\u0BB5\u0BB1\u0BBE\u0BA9 \u0BB5\u0BBF\u0BB0\u0BC1\u0BAA\u0BCD\u0BAA\u0BAE\u0BCD: \u0B8E\u0BA4\u0BBF\u0BB0\u0BCD\u0BAA\u0BBE\u0BB0\u0BCD\u0B95\u0BCD\u0B95\u0BAA\u0BCD\u0BAA\u0B9F\u0BCD\u0B9F\u0BA4\u0BC1 ${T(n.values, "|")} \u0B87\u0BB2\u0BCD \u0B92\u0BA9\u0BCD\u0BB1\u0BC1`;
      case "too_big": {
        let i = n.inclusive ? "<=" : "<", s = t(n.origin);
        if (s) return `\u0BAE\u0BBF\u0B95 \u0BAA\u0BC6\u0BB0\u0BBF\u0BAF\u0BA4\u0BC1: \u0B8E\u0BA4\u0BBF\u0BB0\u0BCD\u0BAA\u0BBE\u0BB0\u0BCD\u0B95\u0BCD\u0B95\u0BAA\u0BCD\u0BAA\u0B9F\u0BCD\u0B9F\u0BA4\u0BC1 ${n.origin ?? "\u0BAE\u0BA4\u0BBF\u0BAA\u0BCD\u0BAA\u0BC1"} ${i}${n.maximum.toString()} ${s.unit ?? "\u0B89\u0BB1\u0BC1\u0BAA\u0BCD\u0BAA\u0BC1\u0B95\u0BB3\u0BCD"} \u0B86\u0B95 \u0B87\u0BB0\u0BC1\u0B95\u0BCD\u0B95 \u0BB5\u0BC7\u0BA3\u0BCD\u0B9F\u0BC1\u0BAE\u0BCD`;
        return `\u0BAE\u0BBF\u0B95 \u0BAA\u0BC6\u0BB0\u0BBF\u0BAF\u0BA4\u0BC1: \u0B8E\u0BA4\u0BBF\u0BB0\u0BCD\u0BAA\u0BBE\u0BB0\u0BCD\u0B95\u0BCD\u0B95\u0BAA\u0BCD\u0BAA\u0B9F\u0BCD\u0B9F\u0BA4\u0BC1 ${n.origin ?? "\u0BAE\u0BA4\u0BBF\u0BAA\u0BCD\u0BAA\u0BC1"} ${i}${n.maximum.toString()} \u0B86\u0B95 \u0B87\u0BB0\u0BC1\u0B95\u0BCD\u0B95 \u0BB5\u0BC7\u0BA3\u0BCD\u0B9F\u0BC1\u0BAE\u0BCD`;
      }
      case "too_small": {
        let i = n.inclusive ? ">=" : ">", s = t(n.origin);
        if (s) return `\u0BAE\u0BBF\u0B95\u0B9A\u0BCD \u0B9A\u0BBF\u0BB1\u0BBF\u0BAF\u0BA4\u0BC1: \u0B8E\u0BA4\u0BBF\u0BB0\u0BCD\u0BAA\u0BBE\u0BB0\u0BCD\u0B95\u0BCD\u0B95\u0BAA\u0BCD\u0BAA\u0B9F\u0BCD\u0B9F\u0BA4\u0BC1 ${n.origin} ${i}${n.minimum.toString()} ${s.unit} \u0B86\u0B95 \u0B87\u0BB0\u0BC1\u0B95\u0BCD\u0B95 \u0BB5\u0BC7\u0BA3\u0BCD\u0B9F\u0BC1\u0BAE\u0BCD`;
        return `\u0BAE\u0BBF\u0B95\u0B9A\u0BCD \u0B9A\u0BBF\u0BB1\u0BBF\u0BAF\u0BA4\u0BC1: \u0B8E\u0BA4\u0BBF\u0BB0\u0BCD\u0BAA\u0BBE\u0BB0\u0BCD\u0B95\u0BCD\u0B95\u0BAA\u0BCD\u0BAA\u0B9F\u0BCD\u0B9F\u0BA4\u0BC1 ${n.origin} ${i}${n.minimum.toString()} \u0B86\u0B95 \u0B87\u0BB0\u0BC1\u0B95\u0BCD\u0B95 \u0BB5\u0BC7\u0BA3\u0BCD\u0B9F\u0BC1\u0BAE\u0BCD`;
      }
      case "invalid_format": {
        let i = n;
        if (i.format === "starts_with") return `\u0BA4\u0BB5\u0BB1\u0BBE\u0BA9 \u0B9A\u0BB0\u0BAE\u0BCD: "${i.prefix}" \u0B87\u0BB2\u0BCD \u0BA4\u0BCA\u0B9F\u0B99\u0BCD\u0B95 \u0BB5\u0BC7\u0BA3\u0BCD\u0B9F\u0BC1\u0BAE\u0BCD`;
        if (i.format === "ends_with") return `\u0BA4\u0BB5\u0BB1\u0BBE\u0BA9 \u0B9A\u0BB0\u0BAE\u0BCD: "${i.suffix}" \u0B87\u0BB2\u0BCD \u0BAE\u0BC1\u0B9F\u0BBF\u0BB5\u0B9F\u0BC8\u0BAF \u0BB5\u0BC7\u0BA3\u0BCD\u0B9F\u0BC1\u0BAE\u0BCD`;
        if (i.format === "includes") return `\u0BA4\u0BB5\u0BB1\u0BBE\u0BA9 \u0B9A\u0BB0\u0BAE\u0BCD: "${i.includes}" \u0B90 \u0B89\u0BB3\u0BCD\u0BB3\u0B9F\u0B95\u0BCD\u0B95 \u0BB5\u0BC7\u0BA3\u0BCD\u0B9F\u0BC1\u0BAE\u0BCD`;
        if (i.format === "regex") return `\u0BA4\u0BB5\u0BB1\u0BBE\u0BA9 \u0B9A\u0BB0\u0BAE\u0BCD: ${i.pattern} \u0BAE\u0BC1\u0BB1\u0BC8\u0BAA\u0BBE\u0B9F\u0BCD\u0B9F\u0BC1\u0B9F\u0BA9\u0BCD \u0BAA\u0BCA\u0BB0\u0BC1\u0BA8\u0BCD\u0BA4 \u0BB5\u0BC7\u0BA3\u0BCD\u0B9F\u0BC1\u0BAE\u0BCD`;
        return `\u0BA4\u0BB5\u0BB1\u0BBE\u0BA9 ${o[i.format] ?? n.format}`;
      }
      case "not_multiple_of":
        return `\u0BA4\u0BB5\u0BB1\u0BBE\u0BA9 \u0B8E\u0BA3\u0BCD: ${n.divisor} \u0B87\u0BA9\u0BCD \u0BAA\u0BB2\u0BAE\u0BBE\u0B95 \u0B87\u0BB0\u0BC1\u0B95\u0BCD\u0B95 \u0BB5\u0BC7\u0BA3\u0BCD\u0B9F\u0BC1\u0BAE\u0BCD`;
      case "unrecognized_keys":
        return `\u0B85\u0B9F\u0BC8\u0BAF\u0BBE\u0BB3\u0BAE\u0BCD \u0BA4\u0BC6\u0BB0\u0BBF\u0BAF\u0BBE\u0BA4 \u0BB5\u0BBF\u0B9A\u0BC8${n.keys.length > 1 ? "\u0B95\u0BB3\u0BCD" : ""}: ${T(n.keys, ", ")}`;
      case "invalid_key":
        return `${n.origin} \u0B87\u0BB2\u0BCD \u0BA4\u0BB5\u0BB1\u0BBE\u0BA9 \u0BB5\u0BBF\u0B9A\u0BC8`;
      case "invalid_union":
        return "\u0BA4\u0BB5\u0BB1\u0BBE\u0BA9 \u0B89\u0BB3\u0BCD\u0BB3\u0BC0\u0B9F\u0BC1";
      case "invalid_element":
        return `${n.origin} \u0B87\u0BB2\u0BCD \u0BA4\u0BB5\u0BB1\u0BBE\u0BA9 \u0BAE\u0BA4\u0BBF\u0BAA\u0BCD\u0BAA\u0BC1`;
      default:
        return "\u0BA4\u0BB5\u0BB1\u0BBE\u0BA9 \u0B89\u0BB3\u0BCD\u0BB3\u0BC0\u0B9F\u0BC1";
    }
  };
};
function U_() {
  return { localeError: NV() };
}
var jV = () => {
  let e = { string: { unit: "\u0E15\u0E31\u0E27\u0E2D\u0E31\u0E01\u0E29\u0E23", verb: "\u0E04\u0E27\u0E23\u0E21\u0E35" }, file: { unit: "\u0E44\u0E1A\u0E15\u0E4C", verb: "\u0E04\u0E27\u0E23\u0E21\u0E35" }, array: { unit: "\u0E23\u0E32\u0E22\u0E01\u0E32\u0E23", verb: "\u0E04\u0E27\u0E23\u0E21\u0E35" }, set: { unit: "\u0E23\u0E32\u0E22\u0E01\u0E32\u0E23", verb: "\u0E04\u0E27\u0E23\u0E21\u0E35" } };
  function t(n) {
    return e[n] ?? null;
  }
  let r = (n) => {
    let i = typeof n;
    switch (i) {
      case "number":
        return Number.isNaN(n) ? "\u0E44\u0E21\u0E48\u0E43\u0E0A\u0E48\u0E15\u0E31\u0E27\u0E40\u0E25\u0E02 (NaN)" : "\u0E15\u0E31\u0E27\u0E40\u0E25\u0E02";
      case "object": {
        if (Array.isArray(n)) return "\u0E2D\u0E32\u0E23\u0E4C\u0E40\u0E23\u0E22\u0E4C (Array)";
        if (n === null) return "\u0E44\u0E21\u0E48\u0E21\u0E35\u0E04\u0E48\u0E32 (null)";
        if (Object.getPrototypeOf(n) !== Object.prototype && n.constructor) return n.constructor.name;
      }
    }
    return i;
  }, o = { regex: "\u0E02\u0E49\u0E2D\u0E21\u0E39\u0E25\u0E17\u0E35\u0E48\u0E1B\u0E49\u0E2D\u0E19", email: "\u0E17\u0E35\u0E48\u0E2D\u0E22\u0E39\u0E48\u0E2D\u0E35\u0E40\u0E21\u0E25", url: "URL", emoji: "\u0E2D\u0E34\u0E42\u0E21\u0E08\u0E34", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "\u0E27\u0E31\u0E19\u0E17\u0E35\u0E48\u0E40\u0E27\u0E25\u0E32\u0E41\u0E1A\u0E1A ISO", date: "\u0E27\u0E31\u0E19\u0E17\u0E35\u0E48\u0E41\u0E1A\u0E1A ISO", time: "\u0E40\u0E27\u0E25\u0E32\u0E41\u0E1A\u0E1A ISO", duration: "\u0E0A\u0E48\u0E27\u0E07\u0E40\u0E27\u0E25\u0E32\u0E41\u0E1A\u0E1A ISO", ipv4: "\u0E17\u0E35\u0E48\u0E2D\u0E22\u0E39\u0E48 IPv4", ipv6: "\u0E17\u0E35\u0E48\u0E2D\u0E22\u0E39\u0E48 IPv6", cidrv4: "\u0E0A\u0E48\u0E27\u0E07 IP \u0E41\u0E1A\u0E1A IPv4", cidrv6: "\u0E0A\u0E48\u0E27\u0E07 IP \u0E41\u0E1A\u0E1A IPv6", base64: "\u0E02\u0E49\u0E2D\u0E04\u0E27\u0E32\u0E21\u0E41\u0E1A\u0E1A Base64", base64url: "\u0E02\u0E49\u0E2D\u0E04\u0E27\u0E32\u0E21\u0E41\u0E1A\u0E1A Base64 \u0E2A\u0E33\u0E2B\u0E23\u0E31\u0E1A URL", json_string: "\u0E02\u0E49\u0E2D\u0E04\u0E27\u0E32\u0E21\u0E41\u0E1A\u0E1A JSON", e164: "\u0E40\u0E1A\u0E2D\u0E23\u0E4C\u0E42\u0E17\u0E23\u0E28\u0E31\u0E1E\u0E17\u0E4C\u0E23\u0E30\u0E2B\u0E27\u0E48\u0E32\u0E07\u0E1B\u0E23\u0E30\u0E40\u0E17\u0E28 (E.164)", jwt: "\u0E42\u0E17\u0E40\u0E04\u0E19 JWT", template_literal: "\u0E02\u0E49\u0E2D\u0E21\u0E39\u0E25\u0E17\u0E35\u0E48\u0E1B\u0E49\u0E2D\u0E19" };
  return (n) => {
    switch (n.code) {
      case "invalid_type":
        return `\u0E1B\u0E23\u0E30\u0E40\u0E20\u0E17\u0E02\u0E49\u0E2D\u0E21\u0E39\u0E25\u0E44\u0E21\u0E48\u0E16\u0E39\u0E01\u0E15\u0E49\u0E2D\u0E07: \u0E04\u0E27\u0E23\u0E40\u0E1B\u0E47\u0E19 ${n.expected} \u0E41\u0E15\u0E48\u0E44\u0E14\u0E49\u0E23\u0E31\u0E1A ${r(n.input)}`;
      case "invalid_value":
        if (n.values.length === 1) return `\u0E04\u0E48\u0E32\u0E44\u0E21\u0E48\u0E16\u0E39\u0E01\u0E15\u0E49\u0E2D\u0E07: \u0E04\u0E27\u0E23\u0E40\u0E1B\u0E47\u0E19 ${D(n.values[0])}`;
        return `\u0E15\u0E31\u0E27\u0E40\u0E25\u0E37\u0E2D\u0E01\u0E44\u0E21\u0E48\u0E16\u0E39\u0E01\u0E15\u0E49\u0E2D\u0E07: \u0E04\u0E27\u0E23\u0E40\u0E1B\u0E47\u0E19\u0E2B\u0E19\u0E36\u0E48\u0E07\u0E43\u0E19 ${T(n.values, "|")}`;
      case "too_big": {
        let i = n.inclusive ? "\u0E44\u0E21\u0E48\u0E40\u0E01\u0E34\u0E19" : "\u0E19\u0E49\u0E2D\u0E22\u0E01\u0E27\u0E48\u0E32", s = t(n.origin);
        if (s) return `\u0E40\u0E01\u0E34\u0E19\u0E01\u0E33\u0E2B\u0E19\u0E14: ${n.origin ?? "\u0E04\u0E48\u0E32"} \u0E04\u0E27\u0E23\u0E21\u0E35${i} ${n.maximum.toString()} ${s.unit ?? "\u0E23\u0E32\u0E22\u0E01\u0E32\u0E23"}`;
        return `\u0E40\u0E01\u0E34\u0E19\u0E01\u0E33\u0E2B\u0E19\u0E14: ${n.origin ?? "\u0E04\u0E48\u0E32"} \u0E04\u0E27\u0E23\u0E21\u0E35${i} ${n.maximum.toString()}`;
      }
      case "too_small": {
        let i = n.inclusive ? "\u0E2D\u0E22\u0E48\u0E32\u0E07\u0E19\u0E49\u0E2D\u0E22" : "\u0E21\u0E32\u0E01\u0E01\u0E27\u0E48\u0E32", s = t(n.origin);
        if (s) return `\u0E19\u0E49\u0E2D\u0E22\u0E01\u0E27\u0E48\u0E32\u0E01\u0E33\u0E2B\u0E19\u0E14: ${n.origin} \u0E04\u0E27\u0E23\u0E21\u0E35${i} ${n.minimum.toString()} ${s.unit}`;
        return `\u0E19\u0E49\u0E2D\u0E22\u0E01\u0E27\u0E48\u0E32\u0E01\u0E33\u0E2B\u0E19\u0E14: ${n.origin} \u0E04\u0E27\u0E23\u0E21\u0E35${i} ${n.minimum.toString()}`;
      }
      case "invalid_format": {
        let i = n;
        if (i.format === "starts_with") return `\u0E23\u0E39\u0E1B\u0E41\u0E1A\u0E1A\u0E44\u0E21\u0E48\u0E16\u0E39\u0E01\u0E15\u0E49\u0E2D\u0E07: \u0E02\u0E49\u0E2D\u0E04\u0E27\u0E32\u0E21\u0E15\u0E49\u0E2D\u0E07\u0E02\u0E36\u0E49\u0E19\u0E15\u0E49\u0E19\u0E14\u0E49\u0E27\u0E22 "${i.prefix}"`;
        if (i.format === "ends_with") return `\u0E23\u0E39\u0E1B\u0E41\u0E1A\u0E1A\u0E44\u0E21\u0E48\u0E16\u0E39\u0E01\u0E15\u0E49\u0E2D\u0E07: \u0E02\u0E49\u0E2D\u0E04\u0E27\u0E32\u0E21\u0E15\u0E49\u0E2D\u0E07\u0E25\u0E07\u0E17\u0E49\u0E32\u0E22\u0E14\u0E49\u0E27\u0E22 "${i.suffix}"`;
        if (i.format === "includes") return `\u0E23\u0E39\u0E1B\u0E41\u0E1A\u0E1A\u0E44\u0E21\u0E48\u0E16\u0E39\u0E01\u0E15\u0E49\u0E2D\u0E07: \u0E02\u0E49\u0E2D\u0E04\u0E27\u0E32\u0E21\u0E15\u0E49\u0E2D\u0E07\u0E21\u0E35 "${i.includes}" \u0E2D\u0E22\u0E39\u0E48\u0E43\u0E19\u0E02\u0E49\u0E2D\u0E04\u0E27\u0E32\u0E21`;
        if (i.format === "regex") return `\u0E23\u0E39\u0E1B\u0E41\u0E1A\u0E1A\u0E44\u0E21\u0E48\u0E16\u0E39\u0E01\u0E15\u0E49\u0E2D\u0E07: \u0E15\u0E49\u0E2D\u0E07\u0E15\u0E23\u0E07\u0E01\u0E31\u0E1A\u0E23\u0E39\u0E1B\u0E41\u0E1A\u0E1A\u0E17\u0E35\u0E48\u0E01\u0E33\u0E2B\u0E19\u0E14 ${i.pattern}`;
        return `\u0E23\u0E39\u0E1B\u0E41\u0E1A\u0E1A\u0E44\u0E21\u0E48\u0E16\u0E39\u0E01\u0E15\u0E49\u0E2D\u0E07: ${o[i.format] ?? n.format}`;
      }
      case "not_multiple_of":
        return `\u0E15\u0E31\u0E27\u0E40\u0E25\u0E02\u0E44\u0E21\u0E48\u0E16\u0E39\u0E01\u0E15\u0E49\u0E2D\u0E07: \u0E15\u0E49\u0E2D\u0E07\u0E40\u0E1B\u0E47\u0E19\u0E08\u0E33\u0E19\u0E27\u0E19\u0E17\u0E35\u0E48\u0E2B\u0E32\u0E23\u0E14\u0E49\u0E27\u0E22 ${n.divisor} \u0E44\u0E14\u0E49\u0E25\u0E07\u0E15\u0E31\u0E27`;
      case "unrecognized_keys":
        return `\u0E1E\u0E1A\u0E04\u0E35\u0E22\u0E4C\u0E17\u0E35\u0E48\u0E44\u0E21\u0E48\u0E23\u0E39\u0E49\u0E08\u0E31\u0E01: ${T(n.keys, ", ")}`;
      case "invalid_key":
        return `\u0E04\u0E35\u0E22\u0E4C\u0E44\u0E21\u0E48\u0E16\u0E39\u0E01\u0E15\u0E49\u0E2D\u0E07\u0E43\u0E19 ${n.origin}`;
      case "invalid_union":
        return "\u0E02\u0E49\u0E2D\u0E21\u0E39\u0E25\u0E44\u0E21\u0E48\u0E16\u0E39\u0E01\u0E15\u0E49\u0E2D\u0E07: \u0E44\u0E21\u0E48\u0E15\u0E23\u0E07\u0E01\u0E31\u0E1A\u0E23\u0E39\u0E1B\u0E41\u0E1A\u0E1A\u0E22\u0E39\u0E40\u0E19\u0E35\u0E22\u0E19\u0E17\u0E35\u0E48\u0E01\u0E33\u0E2B\u0E19\u0E14\u0E44\u0E27\u0E49";
      case "invalid_element":
        return `\u0E02\u0E49\u0E2D\u0E21\u0E39\u0E25\u0E44\u0E21\u0E48\u0E16\u0E39\u0E01\u0E15\u0E49\u0E2D\u0E07\u0E43\u0E19 ${n.origin}`;
      default:
        return "\u0E02\u0E49\u0E2D\u0E21\u0E39\u0E25\u0E44\u0E21\u0E48\u0E16\u0E39\u0E01\u0E15\u0E49\u0E2D\u0E07";
    }
  };
};
function z_() {
  return { localeError: jV() };
}
var UV = (e) => {
  let t = typeof e;
  switch (t) {
    case "number":
      return Number.isNaN(e) ? "NaN" : "number";
    case "object": {
      if (Array.isArray(e)) return "array";
      if (e === null) return "null";
      if (Object.getPrototypeOf(e) !== Object.prototype && e.constructor) return e.constructor.name;
    }
  }
  return t;
};
var zV = () => {
  let e = { string: { unit: "karakter", verb: "olmal\u0131" }, file: { unit: "bayt", verb: "olmal\u0131" }, array: { unit: "\xF6\u011Fe", verb: "olmal\u0131" }, set: { unit: "\xF6\u011Fe", verb: "olmal\u0131" } };
  function t(o) {
    return e[o] ?? null;
  }
  let r = { regex: "girdi", email: "e-posta adresi", url: "URL", emoji: "emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "ISO tarih ve saat", date: "ISO tarih", time: "ISO saat", duration: "ISO s\xFCre", ipv4: "IPv4 adresi", ipv6: "IPv6 adresi", cidrv4: "IPv4 aral\u0131\u011F\u0131", cidrv6: "IPv6 aral\u0131\u011F\u0131", base64: "base64 ile \u015Fifrelenmi\u015F metin", base64url: "base64url ile \u015Fifrelenmi\u015F metin", json_string: "JSON dizesi", e164: "E.164 say\u0131s\u0131", jwt: "JWT", template_literal: "\u015Eablon dizesi" };
  return (o) => {
    switch (o.code) {
      case "invalid_type":
        return `Ge\xE7ersiz de\u011Fer: beklenen ${o.expected}, al\u0131nan ${UV(o.input)}`;
      case "invalid_value":
        if (o.values.length === 1) return `Ge\xE7ersiz de\u011Fer: beklenen ${D(o.values[0])}`;
        return `Ge\xE7ersiz se\xE7enek: a\u015Fa\u011F\u0131dakilerden biri olmal\u0131: ${T(o.values, "|")}`;
      case "too_big": {
        let n = o.inclusive ? "<=" : "<", i = t(o.origin);
        if (i) return `\xC7ok b\xFCy\xFCk: beklenen ${o.origin ?? "de\u011Fer"} ${n}${o.maximum.toString()} ${i.unit ?? "\xF6\u011Fe"}`;
        return `\xC7ok b\xFCy\xFCk: beklenen ${o.origin ?? "de\u011Fer"} ${n}${o.maximum.toString()}`;
      }
      case "too_small": {
        let n = o.inclusive ? ">=" : ">", i = t(o.origin);
        if (i) return `\xC7ok k\xFC\xE7\xFCk: beklenen ${o.origin} ${n}${o.minimum.toString()} ${i.unit}`;
        return `\xC7ok k\xFC\xE7\xFCk: beklenen ${o.origin} ${n}${o.minimum.toString()}`;
      }
      case "invalid_format": {
        let n = o;
        if (n.format === "starts_with") return `Ge\xE7ersiz metin: "${n.prefix}" ile ba\u015Flamal\u0131`;
        if (n.format === "ends_with") return `Ge\xE7ersiz metin: "${n.suffix}" ile bitmeli`;
        if (n.format === "includes") return `Ge\xE7ersiz metin: "${n.includes}" i\xE7ermeli`;
        if (n.format === "regex") return `Ge\xE7ersiz metin: ${n.pattern} desenine uymal\u0131`;
        return `Ge\xE7ersiz ${r[n.format] ?? o.format}`;
      }
      case "not_multiple_of":
        return `Ge\xE7ersiz say\u0131: ${o.divisor} ile tam b\xF6l\xFCnebilmeli`;
      case "unrecognized_keys":
        return `Tan\u0131nmayan anahtar${o.keys.length > 1 ? "lar" : ""}: ${T(o.keys, ", ")}`;
      case "invalid_key":
        return `${o.origin} i\xE7inde ge\xE7ersiz anahtar`;
      case "invalid_union":
        return "Ge\xE7ersiz de\u011Fer";
      case "invalid_element":
        return `${o.origin} i\xE7inde ge\xE7ersiz de\u011Fer`;
      default:
        return "Ge\xE7ersiz de\u011Fer";
    }
  };
};
function L_() {
  return { localeError: zV() };
}
var LV = () => {
  let e = { string: { unit: "\u0441\u0438\u043C\u0432\u043E\u043B\u0456\u0432", verb: "\u043C\u0430\u0442\u0438\u043C\u0435" }, file: { unit: "\u0431\u0430\u0439\u0442\u0456\u0432", verb: "\u043C\u0430\u0442\u0438\u043C\u0435" }, array: { unit: "\u0435\u043B\u0435\u043C\u0435\u043D\u0442\u0456\u0432", verb: "\u043C\u0430\u0442\u0438\u043C\u0435" }, set: { unit: "\u0435\u043B\u0435\u043C\u0435\u043D\u0442\u0456\u0432", verb: "\u043C\u0430\u0442\u0438\u043C\u0435" } };
  function t(n) {
    return e[n] ?? null;
  }
  let r = (n) => {
    let i = typeof n;
    switch (i) {
      case "number":
        return Number.isNaN(n) ? "NaN" : "\u0447\u0438\u0441\u043B\u043E";
      case "object": {
        if (Array.isArray(n)) return "\u043C\u0430\u0441\u0438\u0432";
        if (n === null) return "null";
        if (Object.getPrototypeOf(n) !== Object.prototype && n.constructor) return n.constructor.name;
      }
    }
    return i;
  }, o = { regex: "\u0432\u0445\u0456\u0434\u043D\u0456 \u0434\u0430\u043D\u0456", email: "\u0430\u0434\u0440\u0435\u0441\u0430 \u0435\u043B\u0435\u043A\u0442\u0440\u043E\u043D\u043D\u043E\u0457 \u043F\u043E\u0448\u0442\u0438", url: "URL", emoji: "\u0435\u043C\u043E\u0434\u0437\u0456", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "\u0434\u0430\u0442\u0430 \u0442\u0430 \u0447\u0430\u0441 ISO", date: "\u0434\u0430\u0442\u0430 ISO", time: "\u0447\u0430\u0441 ISO", duration: "\u0442\u0440\u0438\u0432\u0430\u043B\u0456\u0441\u0442\u044C ISO", ipv4: "\u0430\u0434\u0440\u0435\u0441\u0430 IPv4", ipv6: "\u0430\u0434\u0440\u0435\u0441\u0430 IPv6", cidrv4: "\u0434\u0456\u0430\u043F\u0430\u0437\u043E\u043D IPv4", cidrv6: "\u0434\u0456\u0430\u043F\u0430\u0437\u043E\u043D IPv6", base64: "\u0440\u044F\u0434\u043E\u043A \u0443 \u043A\u043E\u0434\u0443\u0432\u0430\u043D\u043D\u0456 base64", base64url: "\u0440\u044F\u0434\u043E\u043A \u0443 \u043A\u043E\u0434\u0443\u0432\u0430\u043D\u043D\u0456 base64url", json_string: "\u0440\u044F\u0434\u043E\u043A JSON", e164: "\u043D\u043E\u043C\u0435\u0440 E.164", jwt: "JWT", template_literal: "\u0432\u0445\u0456\u0434\u043D\u0456 \u0434\u0430\u043D\u0456" };
  return (n) => {
    switch (n.code) {
      case "invalid_type":
        return `\u041D\u0435\u043F\u0440\u0430\u0432\u0438\u043B\u044C\u043D\u0456 \u0432\u0445\u0456\u0434\u043D\u0456 \u0434\u0430\u043D\u0456: \u043E\u0447\u0456\u043A\u0443\u0454\u0442\u044C\u0441\u044F ${n.expected}, \u043E\u0442\u0440\u0438\u043C\u0430\u043D\u043E ${r(n.input)}`;
      case "invalid_value":
        if (n.values.length === 1) return `\u041D\u0435\u043F\u0440\u0430\u0432\u0438\u043B\u044C\u043D\u0456 \u0432\u0445\u0456\u0434\u043D\u0456 \u0434\u0430\u043D\u0456: \u043E\u0447\u0456\u043A\u0443\u0454\u0442\u044C\u0441\u044F ${D(n.values[0])}`;
        return `\u041D\u0435\u043F\u0440\u0430\u0432\u0438\u043B\u044C\u043D\u0430 \u043E\u043F\u0446\u0456\u044F: \u043E\u0447\u0456\u043A\u0443\u0454\u0442\u044C\u0441\u044F \u043E\u0434\u043D\u0435 \u0437 ${T(n.values, "|")}`;
      case "too_big": {
        let i = n.inclusive ? "<=" : "<", s = t(n.origin);
        if (s) return `\u0417\u0430\u043D\u0430\u0434\u0442\u043E \u0432\u0435\u043B\u0438\u043A\u0435: \u043E\u0447\u0456\u043A\u0443\u0454\u0442\u044C\u0441\u044F, \u0449\u043E ${n.origin ?? "\u0437\u043D\u0430\u0447\u0435\u043D\u043D\u044F"} ${s.verb} ${i}${n.maximum.toString()} ${s.unit ?? "\u0435\u043B\u0435\u043C\u0435\u043D\u0442\u0456\u0432"}`;
        return `\u0417\u0430\u043D\u0430\u0434\u0442\u043E \u0432\u0435\u043B\u0438\u043A\u0435: \u043E\u0447\u0456\u043A\u0443\u0454\u0442\u044C\u0441\u044F, \u0449\u043E ${n.origin ?? "\u0437\u043D\u0430\u0447\u0435\u043D\u043D\u044F"} \u0431\u0443\u0434\u0435 ${i}${n.maximum.toString()}`;
      }
      case "too_small": {
        let i = n.inclusive ? ">=" : ">", s = t(n.origin);
        if (s) return `\u0417\u0430\u043D\u0430\u0434\u0442\u043E \u043C\u0430\u043B\u0435: \u043E\u0447\u0456\u043A\u0443\u0454\u0442\u044C\u0441\u044F, \u0449\u043E ${n.origin} ${s.verb} ${i}${n.minimum.toString()} ${s.unit}`;
        return `\u0417\u0430\u043D\u0430\u0434\u0442\u043E \u043C\u0430\u043B\u0435: \u043E\u0447\u0456\u043A\u0443\u0454\u0442\u044C\u0441\u044F, \u0449\u043E ${n.origin} \u0431\u0443\u0434\u0435 ${i}${n.minimum.toString()}`;
      }
      case "invalid_format": {
        let i = n;
        if (i.format === "starts_with") return `\u041D\u0435\u043F\u0440\u0430\u0432\u0438\u043B\u044C\u043D\u0438\u0439 \u0440\u044F\u0434\u043E\u043A: \u043F\u043E\u0432\u0438\u043D\u0435\u043D \u043F\u043E\u0447\u0438\u043D\u0430\u0442\u0438\u0441\u044F \u0437 "${i.prefix}"`;
        if (i.format === "ends_with") return `\u041D\u0435\u043F\u0440\u0430\u0432\u0438\u043B\u044C\u043D\u0438\u0439 \u0440\u044F\u0434\u043E\u043A: \u043F\u043E\u0432\u0438\u043D\u0435\u043D \u0437\u0430\u043A\u0456\u043D\u0447\u0443\u0432\u0430\u0442\u0438\u0441\u044F \u043D\u0430 "${i.suffix}"`;
        if (i.format === "includes") return `\u041D\u0435\u043F\u0440\u0430\u0432\u0438\u043B\u044C\u043D\u0438\u0439 \u0440\u044F\u0434\u043E\u043A: \u043F\u043E\u0432\u0438\u043D\u0435\u043D \u043C\u0456\u0441\u0442\u0438\u0442\u0438 "${i.includes}"`;
        if (i.format === "regex") return `\u041D\u0435\u043F\u0440\u0430\u0432\u0438\u043B\u044C\u043D\u0438\u0439 \u0440\u044F\u0434\u043E\u043A: \u043F\u043E\u0432\u0438\u043D\u0435\u043D \u0432\u0456\u0434\u043F\u043E\u0432\u0456\u0434\u0430\u0442\u0438 \u0448\u0430\u0431\u043B\u043E\u043D\u0443 ${i.pattern}`;
        return `\u041D\u0435\u043F\u0440\u0430\u0432\u0438\u043B\u044C\u043D\u0438\u0439 ${o[i.format] ?? n.format}`;
      }
      case "not_multiple_of":
        return `\u041D\u0435\u043F\u0440\u0430\u0432\u0438\u043B\u044C\u043D\u0435 \u0447\u0438\u0441\u043B\u043E: \u043F\u043E\u0432\u0438\u043D\u043D\u043E \u0431\u0443\u0442\u0438 \u043A\u0440\u0430\u0442\u043D\u0438\u043C ${n.divisor}`;
      case "unrecognized_keys":
        return `\u041D\u0435\u0440\u043E\u0437\u043F\u0456\u0437\u043D\u0430\u043D\u0438\u0439 \u043A\u043B\u044E\u0447${n.keys.length > 1 ? "\u0456" : ""}: ${T(n.keys, ", ")}`;
      case "invalid_key":
        return `\u041D\u0435\u043F\u0440\u0430\u0432\u0438\u043B\u044C\u043D\u0438\u0439 \u043A\u043B\u044E\u0447 \u0443 ${n.origin}`;
      case "invalid_union":
        return "\u041D\u0435\u043F\u0440\u0430\u0432\u0438\u043B\u044C\u043D\u0456 \u0432\u0445\u0456\u0434\u043D\u0456 \u0434\u0430\u043D\u0456";
      case "invalid_element":
        return `\u041D\u0435\u043F\u0440\u0430\u0432\u0438\u043B\u044C\u043D\u0435 \u0437\u043D\u0430\u0447\u0435\u043D\u043D\u044F \u0443 ${n.origin}`;
      default:
        return "\u041D\u0435\u043F\u0440\u0430\u0432\u0438\u043B\u044C\u043D\u0456 \u0432\u0445\u0456\u0434\u043D\u0456 \u0434\u0430\u043D\u0456";
    }
  };
};
function F_() {
  return { localeError: LV() };
}
var FV = () => {
  let e = { string: { unit: "\u062D\u0631\u0648\u0641", verb: "\u06C1\u0648\u0646\u0627" }, file: { unit: "\u0628\u0627\u0626\u0679\u0633", verb: "\u06C1\u0648\u0646\u0627" }, array: { unit: "\u0622\u0626\u0679\u0645\u0632", verb: "\u06C1\u0648\u0646\u0627" }, set: { unit: "\u0622\u0626\u0679\u0645\u0632", verb: "\u06C1\u0648\u0646\u0627" } };
  function t(n) {
    return e[n] ?? null;
  }
  let r = (n) => {
    let i = typeof n;
    switch (i) {
      case "number":
        return Number.isNaN(n) ? "NaN" : "\u0646\u0645\u0628\u0631";
      case "object": {
        if (Array.isArray(n)) return "\u0622\u0631\u06D2";
        if (n === null) return "\u0646\u0644";
        if (Object.getPrototypeOf(n) !== Object.prototype && n.constructor) return n.constructor.name;
      }
    }
    return i;
  }, o = { regex: "\u0627\u0646 \u067E\u0679", email: "\u0627\u06CC \u0645\u06CC\u0644 \u0627\u06CC\u0688\u0631\u06CC\u0633", url: "\u06CC\u0648 \u0622\u0631 \u0627\u06CC\u0644", emoji: "\u0627\u06CC\u0645\u0648\u062C\u06CC", uuid: "\u06CC\u0648 \u06CC\u0648 \u0622\u0626\u06CC \u0688\u06CC", uuidv4: "\u06CC\u0648 \u06CC\u0648 \u0622\u0626\u06CC \u0688\u06CC \u0648\u06CC 4", uuidv6: "\u06CC\u0648 \u06CC\u0648 \u0622\u0626\u06CC \u0688\u06CC \u0648\u06CC 6", nanoid: "\u0646\u06CC\u0646\u0648 \u0622\u0626\u06CC \u0688\u06CC", guid: "\u062C\u06CC \u06CC\u0648 \u0622\u0626\u06CC \u0688\u06CC", cuid: "\u0633\u06CC \u06CC\u0648 \u0622\u0626\u06CC \u0688\u06CC", cuid2: "\u0633\u06CC \u06CC\u0648 \u0622\u0626\u06CC \u0688\u06CC 2", ulid: "\u06CC\u0648 \u0627\u06CC\u0644 \u0622\u0626\u06CC \u0688\u06CC", xid: "\u0627\u06CC\u06A9\u0633 \u0622\u0626\u06CC \u0688\u06CC", ksuid: "\u06A9\u06D2 \u0627\u06CC\u0633 \u06CC\u0648 \u0622\u0626\u06CC \u0688\u06CC", datetime: "\u0622\u0626\u06CC \u0627\u06CC\u0633 \u0627\u0648 \u0688\u06CC\u0679 \u0679\u0627\u0626\u0645", date: "\u0622\u0626\u06CC \u0627\u06CC\u0633 \u0627\u0648 \u062A\u0627\u0631\u06CC\u062E", time: "\u0622\u0626\u06CC \u0627\u06CC\u0633 \u0627\u0648 \u0648\u0642\u062A", duration: "\u0622\u0626\u06CC \u0627\u06CC\u0633 \u0627\u0648 \u0645\u062F\u062A", ipv4: "\u0622\u0626\u06CC \u067E\u06CC \u0648\u06CC 4 \u0627\u06CC\u0688\u0631\u06CC\u0633", ipv6: "\u0622\u0626\u06CC \u067E\u06CC \u0648\u06CC 6 \u0627\u06CC\u0688\u0631\u06CC\u0633", cidrv4: "\u0622\u0626\u06CC \u067E\u06CC \u0648\u06CC 4 \u0631\u06CC\u0646\u062C", cidrv6: "\u0622\u0626\u06CC \u067E\u06CC \u0648\u06CC 6 \u0631\u06CC\u0646\u062C", base64: "\u0628\u06CC\u0633 64 \u0627\u0646 \u06A9\u0648\u0688\u0688 \u0633\u0679\u0631\u0646\u06AF", base64url: "\u0628\u06CC\u0633 64 \u06CC\u0648 \u0622\u0631 \u0627\u06CC\u0644 \u0627\u0646 \u06A9\u0648\u0688\u0688 \u0633\u0679\u0631\u0646\u06AF", json_string: "\u062C\u06D2 \u0627\u06CC\u0633 \u0627\u0648 \u0627\u06CC\u0646 \u0633\u0679\u0631\u0646\u06AF", e164: "\u0627\u06CC 164 \u0646\u0645\u0628\u0631", jwt: "\u062C\u06D2 \u0688\u0628\u0644\u06CC\u0648 \u0679\u06CC", template_literal: "\u0627\u0646 \u067E\u0679" };
  return (n) => {
    switch (n.code) {
      case "invalid_type":
        return `\u063A\u0644\u0637 \u0627\u0646 \u067E\u0679: ${n.expected} \u0645\u062A\u0648\u0642\u0639 \u062A\u06BE\u0627\u060C ${r(n.input)} \u0645\u0648\u0635\u0648\u0644 \u06C1\u0648\u0627`;
      case "invalid_value":
        if (n.values.length === 1) return `\u063A\u0644\u0637 \u0627\u0646 \u067E\u0679: ${D(n.values[0])} \u0645\u062A\u0648\u0642\u0639 \u062A\u06BE\u0627`;
        return `\u063A\u0644\u0637 \u0622\u067E\u0634\u0646: ${T(n.values, "|")} \u0645\u06CC\u06BA \u0633\u06D2 \u0627\u06CC\u06A9 \u0645\u062A\u0648\u0642\u0639 \u062A\u06BE\u0627`;
      case "too_big": {
        let i = n.inclusive ? "<=" : "<", s = t(n.origin);
        if (s) return `\u0628\u06C1\u062A \u0628\u0691\u0627: ${n.origin ?? "\u0648\u06CC\u0644\u06CC\u0648"} \u06A9\u06D2 ${i}${n.maximum.toString()} ${s.unit ?? "\u0639\u0646\u0627\u0635\u0631"} \u06C1\u0648\u0646\u06D2 \u0645\u062A\u0648\u0642\u0639 \u062A\u06BE\u06D2`;
        return `\u0628\u06C1\u062A \u0628\u0691\u0627: ${n.origin ?? "\u0648\u06CC\u0644\u06CC\u0648"} \u06A9\u0627 ${i}${n.maximum.toString()} \u06C1\u0648\u0646\u0627 \u0645\u062A\u0648\u0642\u0639 \u062A\u06BE\u0627`;
      }
      case "too_small": {
        let i = n.inclusive ? ">=" : ">", s = t(n.origin);
        if (s) return `\u0628\u06C1\u062A \u0686\u06BE\u0648\u0679\u0627: ${n.origin} \u06A9\u06D2 ${i}${n.minimum.toString()} ${s.unit} \u06C1\u0648\u0646\u06D2 \u0645\u062A\u0648\u0642\u0639 \u062A\u06BE\u06D2`;
        return `\u0628\u06C1\u062A \u0686\u06BE\u0648\u0679\u0627: ${n.origin} \u06A9\u0627 ${i}${n.minimum.toString()} \u06C1\u0648\u0646\u0627 \u0645\u062A\u0648\u0642\u0639 \u062A\u06BE\u0627`;
      }
      case "invalid_format": {
        let i = n;
        if (i.format === "starts_with") return `\u063A\u0644\u0637 \u0633\u0679\u0631\u0646\u06AF: "${i.prefix}" \u0633\u06D2 \u0634\u0631\u0648\u0639 \u06C1\u0648\u0646\u0627 \u0686\u0627\u06C1\u06CC\u06D2`;
        if (i.format === "ends_with") return `\u063A\u0644\u0637 \u0633\u0679\u0631\u0646\u06AF: "${i.suffix}" \u067E\u0631 \u062E\u062A\u0645 \u06C1\u0648\u0646\u0627 \u0686\u0627\u06C1\u06CC\u06D2`;
        if (i.format === "includes") return `\u063A\u0644\u0637 \u0633\u0679\u0631\u0646\u06AF: "${i.includes}" \u0634\u0627\u0645\u0644 \u06C1\u0648\u0646\u0627 \u0686\u0627\u06C1\u06CC\u06D2`;
        if (i.format === "regex") return `\u063A\u0644\u0637 \u0633\u0679\u0631\u0646\u06AF: \u067E\u06CC\u0679\u0631\u0646 ${i.pattern} \u0633\u06D2 \u0645\u06CC\u0686 \u06C1\u0648\u0646\u0627 \u0686\u0627\u06C1\u06CC\u06D2`;
        return `\u063A\u0644\u0637 ${o[i.format] ?? n.format}`;
      }
      case "not_multiple_of":
        return `\u063A\u0644\u0637 \u0646\u0645\u0628\u0631: ${n.divisor} \u06A9\u0627 \u0645\u0636\u0627\u0639\u0641 \u06C1\u0648\u0646\u0627 \u0686\u0627\u06C1\u06CC\u06D2`;
      case "unrecognized_keys":
        return `\u063A\u06CC\u0631 \u062A\u0633\u0644\u06CC\u0645 \u0634\u062F\u06C1 \u06A9\u06CC${n.keys.length > 1 ? "\u0632" : ""}: ${T(n.keys, "\u060C ")}`;
      case "invalid_key":
        return `${n.origin} \u0645\u06CC\u06BA \u063A\u0644\u0637 \u06A9\u06CC`;
      case "invalid_union":
        return "\u063A\u0644\u0637 \u0627\u0646 \u067E\u0679";
      case "invalid_element":
        return `${n.origin} \u0645\u06CC\u06BA \u063A\u0644\u0637 \u0648\u06CC\u0644\u06CC\u0648`;
      default:
        return "\u063A\u0644\u0637 \u0627\u0646 \u067E\u0679";
    }
  };
};
function B_() {
  return { localeError: FV() };
}
var BV = () => {
  let e = { string: { unit: "k\xFD t\u1EF1", verb: "c\xF3" }, file: { unit: "byte", verb: "c\xF3" }, array: { unit: "ph\u1EA7n t\u1EED", verb: "c\xF3" }, set: { unit: "ph\u1EA7n t\u1EED", verb: "c\xF3" } };
  function t(n) {
    return e[n] ?? null;
  }
  let r = (n) => {
    let i = typeof n;
    switch (i) {
      case "number":
        return Number.isNaN(n) ? "NaN" : "s\u1ED1";
      case "object": {
        if (Array.isArray(n)) return "m\u1EA3ng";
        if (n === null) return "null";
        if (Object.getPrototypeOf(n) !== Object.prototype && n.constructor) return n.constructor.name;
      }
    }
    return i;
  }, o = { regex: "\u0111\u1EA7u v\xE0o", email: "\u0111\u1ECBa ch\u1EC9 email", url: "URL", emoji: "emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "ng\xE0y gi\u1EDD ISO", date: "ng\xE0y ISO", time: "gi\u1EDD ISO", duration: "kho\u1EA3ng th\u1EDDi gian ISO", ipv4: "\u0111\u1ECBa ch\u1EC9 IPv4", ipv6: "\u0111\u1ECBa ch\u1EC9 IPv6", cidrv4: "d\u1EA3i IPv4", cidrv6: "d\u1EA3i IPv6", base64: "chu\u1ED7i m\xE3 h\xF3a base64", base64url: "chu\u1ED7i m\xE3 h\xF3a base64url", json_string: "chu\u1ED7i JSON", e164: "s\u1ED1 E.164", jwt: "JWT", template_literal: "\u0111\u1EA7u v\xE0o" };
  return (n) => {
    switch (n.code) {
      case "invalid_type":
        return `\u0110\u1EA7u v\xE0o kh\xF4ng h\u1EE3p l\u1EC7: mong \u0111\u1EE3i ${n.expected}, nh\u1EADn \u0111\u01B0\u1EE3c ${r(n.input)}`;
      case "invalid_value":
        if (n.values.length === 1) return `\u0110\u1EA7u v\xE0o kh\xF4ng h\u1EE3p l\u1EC7: mong \u0111\u1EE3i ${D(n.values[0])}`;
        return `T\xF9y ch\u1ECDn kh\xF4ng h\u1EE3p l\u1EC7: mong \u0111\u1EE3i m\u1ED9t trong c\xE1c gi\xE1 tr\u1ECB ${T(n.values, "|")}`;
      case "too_big": {
        let i = n.inclusive ? "<=" : "<", s = t(n.origin);
        if (s) return `Qu\xE1 l\u1EDBn: mong \u0111\u1EE3i ${n.origin ?? "gi\xE1 tr\u1ECB"} ${s.verb} ${i}${n.maximum.toString()} ${s.unit ?? "ph\u1EA7n t\u1EED"}`;
        return `Qu\xE1 l\u1EDBn: mong \u0111\u1EE3i ${n.origin ?? "gi\xE1 tr\u1ECB"} ${i}${n.maximum.toString()}`;
      }
      case "too_small": {
        let i = n.inclusive ? ">=" : ">", s = t(n.origin);
        if (s) return `Qu\xE1 nh\u1ECF: mong \u0111\u1EE3i ${n.origin} ${s.verb} ${i}${n.minimum.toString()} ${s.unit}`;
        return `Qu\xE1 nh\u1ECF: mong \u0111\u1EE3i ${n.origin} ${i}${n.minimum.toString()}`;
      }
      case "invalid_format": {
        let i = n;
        if (i.format === "starts_with") return `Chu\u1ED7i kh\xF4ng h\u1EE3p l\u1EC7: ph\u1EA3i b\u1EAFt \u0111\u1EA7u b\u1EB1ng "${i.prefix}"`;
        if (i.format === "ends_with") return `Chu\u1ED7i kh\xF4ng h\u1EE3p l\u1EC7: ph\u1EA3i k\u1EBFt th\xFAc b\u1EB1ng "${i.suffix}"`;
        if (i.format === "includes") return `Chu\u1ED7i kh\xF4ng h\u1EE3p l\u1EC7: ph\u1EA3i bao g\u1ED3m "${i.includes}"`;
        if (i.format === "regex") return `Chu\u1ED7i kh\xF4ng h\u1EE3p l\u1EC7: ph\u1EA3i kh\u1EDBp v\u1EDBi m\u1EABu ${i.pattern}`;
        return `${o[i.format] ?? n.format} kh\xF4ng h\u1EE3p l\u1EC7`;
      }
      case "not_multiple_of":
        return `S\u1ED1 kh\xF4ng h\u1EE3p l\u1EC7: ph\u1EA3i l\xE0 b\u1ED9i s\u1ED1 c\u1EE7a ${n.divisor}`;
      case "unrecognized_keys":
        return `Kh\xF3a kh\xF4ng \u0111\u01B0\u1EE3c nh\u1EADn d\u1EA1ng: ${T(n.keys, ", ")}`;
      case "invalid_key":
        return `Kh\xF3a kh\xF4ng h\u1EE3p l\u1EC7 trong ${n.origin}`;
      case "invalid_union":
        return "\u0110\u1EA7u v\xE0o kh\xF4ng h\u1EE3p l\u1EC7";
      case "invalid_element":
        return `Gi\xE1 tr\u1ECB kh\xF4ng h\u1EE3p l\u1EC7 trong ${n.origin}`;
      default:
        return "\u0110\u1EA7u v\xE0o kh\xF4ng h\u1EE3p l\u1EC7";
    }
  };
};
function H_() {
  return { localeError: BV() };
}
var HV = () => {
  let e = { string: { unit: "\u5B57\u7B26", verb: "\u5305\u542B" }, file: { unit: "\u5B57\u8282", verb: "\u5305\u542B" }, array: { unit: "\u9879", verb: "\u5305\u542B" }, set: { unit: "\u9879", verb: "\u5305\u542B" } };
  function t(n) {
    return e[n] ?? null;
  }
  let r = (n) => {
    let i = typeof n;
    switch (i) {
      case "number":
        return Number.isNaN(n) ? "\u975E\u6570\u5B57(NaN)" : "\u6570\u5B57";
      case "object": {
        if (Array.isArray(n)) return "\u6570\u7EC4";
        if (n === null) return "\u7A7A\u503C(null)";
        if (Object.getPrototypeOf(n) !== Object.prototype && n.constructor) return n.constructor.name;
      }
    }
    return i;
  }, o = { regex: "\u8F93\u5165", email: "\u7535\u5B50\u90AE\u4EF6", url: "URL", emoji: "\u8868\u60C5\u7B26\u53F7", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "ISO\u65E5\u671F\u65F6\u95F4", date: "ISO\u65E5\u671F", time: "ISO\u65F6\u95F4", duration: "ISO\u65F6\u957F", ipv4: "IPv4\u5730\u5740", ipv6: "IPv6\u5730\u5740", cidrv4: "IPv4\u7F51\u6BB5", cidrv6: "IPv6\u7F51\u6BB5", base64: "base64\u7F16\u7801\u5B57\u7B26\u4E32", base64url: "base64url\u7F16\u7801\u5B57\u7B26\u4E32", json_string: "JSON\u5B57\u7B26\u4E32", e164: "E.164\u53F7\u7801", jwt: "JWT", template_literal: "\u8F93\u5165" };
  return (n) => {
    switch (n.code) {
      case "invalid_type":
        return `\u65E0\u6548\u8F93\u5165\uFF1A\u671F\u671B ${n.expected}\uFF0C\u5B9E\u9645\u63A5\u6536 ${r(n.input)}`;
      case "invalid_value":
        if (n.values.length === 1) return `\u65E0\u6548\u8F93\u5165\uFF1A\u671F\u671B ${D(n.values[0])}`;
        return `\u65E0\u6548\u9009\u9879\uFF1A\u671F\u671B\u4EE5\u4E0B\u4E4B\u4E00 ${T(n.values, "|")}`;
      case "too_big": {
        let i = n.inclusive ? "<=" : "<", s = t(n.origin);
        if (s) return `\u6570\u503C\u8FC7\u5927\uFF1A\u671F\u671B ${n.origin ?? "\u503C"} ${i}${n.maximum.toString()} ${s.unit ?? "\u4E2A\u5143\u7D20"}`;
        return `\u6570\u503C\u8FC7\u5927\uFF1A\u671F\u671B ${n.origin ?? "\u503C"} ${i}${n.maximum.toString()}`;
      }
      case "too_small": {
        let i = n.inclusive ? ">=" : ">", s = t(n.origin);
        if (s) return `\u6570\u503C\u8FC7\u5C0F\uFF1A\u671F\u671B ${n.origin} ${i}${n.minimum.toString()} ${s.unit}`;
        return `\u6570\u503C\u8FC7\u5C0F\uFF1A\u671F\u671B ${n.origin} ${i}${n.minimum.toString()}`;
      }
      case "invalid_format": {
        let i = n;
        if (i.format === "starts_with") return `\u65E0\u6548\u5B57\u7B26\u4E32\uFF1A\u5FC5\u987B\u4EE5 "${i.prefix}" \u5F00\u5934`;
        if (i.format === "ends_with") return `\u65E0\u6548\u5B57\u7B26\u4E32\uFF1A\u5FC5\u987B\u4EE5 "${i.suffix}" \u7ED3\u5C3E`;
        if (i.format === "includes") return `\u65E0\u6548\u5B57\u7B26\u4E32\uFF1A\u5FC5\u987B\u5305\u542B "${i.includes}"`;
        if (i.format === "regex") return `\u65E0\u6548\u5B57\u7B26\u4E32\uFF1A\u5FC5\u987B\u6EE1\u8DB3\u6B63\u5219\u8868\u8FBE\u5F0F ${i.pattern}`;
        return `\u65E0\u6548${o[i.format] ?? n.format}`;
      }
      case "not_multiple_of":
        return `\u65E0\u6548\u6570\u5B57\uFF1A\u5FC5\u987B\u662F ${n.divisor} \u7684\u500D\u6570`;
      case "unrecognized_keys":
        return `\u51FA\u73B0\u672A\u77E5\u7684\u952E(key): ${T(n.keys, ", ")}`;
      case "invalid_key":
        return `${n.origin} \u4E2D\u7684\u952E(key)\u65E0\u6548`;
      case "invalid_union":
        return "\u65E0\u6548\u8F93\u5165";
      case "invalid_element":
        return `${n.origin} \u4E2D\u5305\u542B\u65E0\u6548\u503C(value)`;
      default:
        return "\u65E0\u6548\u8F93\u5165";
    }
  };
};
function q_() {
  return { localeError: HV() };
}
var qV = () => {
  let e = { string: { unit: "\u5B57\u5143", verb: "\u64C1\u6709" }, file: { unit: "\u4F4D\u5143\u7D44", verb: "\u64C1\u6709" }, array: { unit: "\u9805\u76EE", verb: "\u64C1\u6709" }, set: { unit: "\u9805\u76EE", verb: "\u64C1\u6709" } };
  function t(n) {
    return e[n] ?? null;
  }
  let r = (n) => {
    let i = typeof n;
    switch (i) {
      case "number":
        return Number.isNaN(n) ? "NaN" : "number";
      case "object": {
        if (Array.isArray(n)) return "array";
        if (n === null) return "null";
        if (Object.getPrototypeOf(n) !== Object.prototype && n.constructor) return n.constructor.name;
      }
    }
    return i;
  }, o = { regex: "\u8F38\u5165", email: "\u90F5\u4EF6\u5730\u5740", url: "URL", emoji: "emoji", uuid: "UUID", uuidv4: "UUIDv4", uuidv6: "UUIDv6", nanoid: "nanoid", guid: "GUID", cuid: "cuid", cuid2: "cuid2", ulid: "ULID", xid: "XID", ksuid: "KSUID", datetime: "ISO \u65E5\u671F\u6642\u9593", date: "ISO \u65E5\u671F", time: "ISO \u6642\u9593", duration: "ISO \u671F\u9593", ipv4: "IPv4 \u4F4D\u5740", ipv6: "IPv6 \u4F4D\u5740", cidrv4: "IPv4 \u7BC4\u570D", cidrv6: "IPv6 \u7BC4\u570D", base64: "base64 \u7DE8\u78BC\u5B57\u4E32", base64url: "base64url \u7DE8\u78BC\u5B57\u4E32", json_string: "JSON \u5B57\u4E32", e164: "E.164 \u6578\u503C", jwt: "JWT", template_literal: "\u8F38\u5165" };
  return (n) => {
    switch (n.code) {
      case "invalid_type":
        return `\u7121\u6548\u7684\u8F38\u5165\u503C\uFF1A\u9810\u671F\u70BA ${n.expected}\uFF0C\u4F46\u6536\u5230 ${r(n.input)}`;
      case "invalid_value":
        if (n.values.length === 1) return `\u7121\u6548\u7684\u8F38\u5165\u503C\uFF1A\u9810\u671F\u70BA ${D(n.values[0])}`;
        return `\u7121\u6548\u7684\u9078\u9805\uFF1A\u9810\u671F\u70BA\u4EE5\u4E0B\u5176\u4E2D\u4E4B\u4E00 ${T(n.values, "|")}`;
      case "too_big": {
        let i = n.inclusive ? "<=" : "<", s = t(n.origin);
        if (s) return `\u6578\u503C\u904E\u5927\uFF1A\u9810\u671F ${n.origin ?? "\u503C"} \u61C9\u70BA ${i}${n.maximum.toString()} ${s.unit ?? "\u500B\u5143\u7D20"}`;
        return `\u6578\u503C\u904E\u5927\uFF1A\u9810\u671F ${n.origin ?? "\u503C"} \u61C9\u70BA ${i}${n.maximum.toString()}`;
      }
      case "too_small": {
        let i = n.inclusive ? ">=" : ">", s = t(n.origin);
        if (s) return `\u6578\u503C\u904E\u5C0F\uFF1A\u9810\u671F ${n.origin} \u61C9\u70BA ${i}${n.minimum.toString()} ${s.unit}`;
        return `\u6578\u503C\u904E\u5C0F\uFF1A\u9810\u671F ${n.origin} \u61C9\u70BA ${i}${n.minimum.toString()}`;
      }
      case "invalid_format": {
        let i = n;
        if (i.format === "starts_with") return `\u7121\u6548\u7684\u5B57\u4E32\uFF1A\u5FC5\u9808\u4EE5 "${i.prefix}" \u958B\u982D`;
        if (i.format === "ends_with") return `\u7121\u6548\u7684\u5B57\u4E32\uFF1A\u5FC5\u9808\u4EE5 "${i.suffix}" \u7D50\u5C3E`;
        if (i.format === "includes") return `\u7121\u6548\u7684\u5B57\u4E32\uFF1A\u5FC5\u9808\u5305\u542B "${i.includes}"`;
        if (i.format === "regex") return `\u7121\u6548\u7684\u5B57\u4E32\uFF1A\u5FC5\u9808\u7B26\u5408\u683C\u5F0F ${i.pattern}`;
        return `\u7121\u6548\u7684 ${o[i.format] ?? n.format}`;
      }
      case "not_multiple_of":
        return `\u7121\u6548\u7684\u6578\u5B57\uFF1A\u5FC5\u9808\u70BA ${n.divisor} \u7684\u500D\u6578`;
      case "unrecognized_keys":
        return `\u7121\u6CD5\u8B58\u5225\u7684\u9375\u503C${n.keys.length > 1 ? "\u5011" : ""}\uFF1A${T(n.keys, "\u3001")}`;
      case "invalid_key":
        return `${n.origin} \u4E2D\u6709\u7121\u6548\u7684\u9375\u503C`;
      case "invalid_union":
        return "\u7121\u6548\u7684\u8F38\u5165\u503C";
      case "invalid_element":
        return `${n.origin} \u4E2D\u6709\u7121\u6548\u7684\u503C`;
      default:
        return "\u7121\u6548\u7684\u8F38\u5165\u503C";
    }
  };
};
function Z_() {
  return { localeError: qV() };
}
var rf = Symbol("ZodOutput");
var nf = Symbol("ZodInput");
var $c = class {
  constructor() {
    this._map = /* @__PURE__ */ new WeakMap(), this._idmap = /* @__PURE__ */ new Map();
  }
  add(e, ...t) {
    let r = t[0];
    if (this._map.set(e, r), r && typeof r === "object" && "id" in r) {
      if (this._idmap.has(r.id)) throw Error(`ID ${r.id} already exists in the registry`);
      this._idmap.set(r.id, e);
    }
    return this;
  }
  remove(e) {
    return this._map.delete(e), this;
  }
  get(e) {
    let t = e._zod.parent;
    if (t) {
      let r = { ...this.get(t) ?? {} };
      return delete r.id, { ...r, ...this._map.get(e) };
    }
    return this._map.get(e);
  }
  has(e) {
    return this._map.has(e);
  }
};
function Oc() {
  return new $c();
}
var kt = Oc();
function of(e, t) {
  return new e({ type: "string", ...$(t) });
}
function V_(e, t) {
  return new e({ type: "string", coerce: true, ...$(t) });
}
function Ac(e, t) {
  return new e({ type: "string", format: "email", check: "string_format", abort: false, ...$(t) });
}
function ts(e, t) {
  return new e({ type: "string", format: "guid", check: "string_format", abort: false, ...$(t) });
}
function Cc(e, t) {
  return new e({ type: "string", format: "uuid", check: "string_format", abort: false, ...$(t) });
}
function Mc(e, t) {
  return new e({ type: "string", format: "uuid", check: "string_format", abort: false, version: "v4", ...$(t) });
}
function Dc(e, t) {
  return new e({ type: "string", format: "uuid", check: "string_format", abort: false, version: "v6", ...$(t) });
}
function Nc(e, t) {
  return new e({ type: "string", format: "uuid", check: "string_format", abort: false, version: "v7", ...$(t) });
}
function jc(e, t) {
  return new e({ type: "string", format: "url", check: "string_format", abort: false, ...$(t) });
}
function Uc(e, t) {
  return new e({ type: "string", format: "emoji", check: "string_format", abort: false, ...$(t) });
}
function zc(e, t) {
  return new e({ type: "string", format: "nanoid", check: "string_format", abort: false, ...$(t) });
}
function Lc(e, t) {
  return new e({ type: "string", format: "cuid", check: "string_format", abort: false, ...$(t) });
}
function Fc(e, t) {
  return new e({ type: "string", format: "cuid2", check: "string_format", abort: false, ...$(t) });
}
function Bc(e, t) {
  return new e({ type: "string", format: "ulid", check: "string_format", abort: false, ...$(t) });
}
function Hc(e, t) {
  return new e({ type: "string", format: "xid", check: "string_format", abort: false, ...$(t) });
}
function qc(e, t) {
  return new e({ type: "string", format: "ksuid", check: "string_format", abort: false, ...$(t) });
}
function Zc(e, t) {
  return new e({ type: "string", format: "ipv4", check: "string_format", abort: false, ...$(t) });
}
function Vc(e, t) {
  return new e({ type: "string", format: "ipv6", check: "string_format", abort: false, ...$(t) });
}
function Wc(e, t) {
  return new e({ type: "string", format: "cidrv4", check: "string_format", abort: false, ...$(t) });
}
function Kc(e, t) {
  return new e({ type: "string", format: "cidrv6", check: "string_format", abort: false, ...$(t) });
}
function Gc(e, t) {
  return new e({ type: "string", format: "base64", check: "string_format", abort: false, ...$(t) });
}
function Jc(e, t) {
  return new e({ type: "string", format: "base64url", check: "string_format", abort: false, ...$(t) });
}
function Xc(e, t) {
  return new e({ type: "string", format: "e164", check: "string_format", abort: false, ...$(t) });
}
function Yc(e, t) {
  return new e({ type: "string", format: "jwt", check: "string_format", abort: false, ...$(t) });
}
var sf = { Any: null, Minute: -1, Second: 0, Millisecond: 3, Microsecond: 6 };
function W_(e, t) {
  return new e({ type: "string", format: "datetime", check: "string_format", offset: false, local: false, precision: null, ...$(t) });
}
function K_(e, t) {
  return new e({ type: "string", format: "date", check: "string_format", ...$(t) });
}
function G_(e, t) {
  return new e({ type: "string", format: "time", check: "string_format", precision: null, ...$(t) });
}
function J_(e, t) {
  return new e({ type: "string", format: "duration", check: "string_format", ...$(t) });
}
function af(e, t) {
  return new e({ type: "number", checks: [], ...$(t) });
}
function X_(e, t) {
  return new e({ type: "number", coerce: true, checks: [], ...$(t) });
}
function cf(e, t) {
  return new e({ type: "number", check: "number_format", abort: false, format: "safeint", ...$(t) });
}
function lf(e, t) {
  return new e({ type: "number", check: "number_format", abort: false, format: "float32", ...$(t) });
}
function uf(e, t) {
  return new e({ type: "number", check: "number_format", abort: false, format: "float64", ...$(t) });
}
function df(e, t) {
  return new e({ type: "number", check: "number_format", abort: false, format: "int32", ...$(t) });
}
function pf(e, t) {
  return new e({ type: "number", check: "number_format", abort: false, format: "uint32", ...$(t) });
}
function ff(e, t) {
  return new e({ type: "boolean", ...$(t) });
}
function Y_(e, t) {
  return new e({ type: "boolean", coerce: true, ...$(t) });
}
function mf(e, t) {
  return new e({ type: "bigint", ...$(t) });
}
function Q_(e, t) {
  return new e({ type: "bigint", coerce: true, ...$(t) });
}
function gf(e, t) {
  return new e({ type: "bigint", check: "bigint_format", abort: false, format: "int64", ...$(t) });
}
function hf(e, t) {
  return new e({ type: "bigint", check: "bigint_format", abort: false, format: "uint64", ...$(t) });
}
function yf(e, t) {
  return new e({ type: "symbol", ...$(t) });
}
function bf(e, t) {
  return new e({ type: "undefined", ...$(t) });
}
function _f(e, t) {
  return new e({ type: "null", ...$(t) });
}
function vf(e) {
  return new e({ type: "any" });
}
function Oo(e) {
  return new e({ type: "unknown" });
}
function Sf(e, t) {
  return new e({ type: "never", ...$(t) });
}
function xf(e, t) {
  return new e({ type: "void", ...$(t) });
}
function wf(e, t) {
  return new e({ type: "date", ...$(t) });
}
function ev(e, t) {
  return new e({ type: "date", coerce: true, ...$(t) });
}
function kf(e, t) {
  return new e({ type: "nan", ...$(t) });
}
function Jr(e, t) {
  return new tp({ check: "less_than", ...$(t), value: e, inclusive: false });
}
function Jt(e, t) {
  return new tp({ check: "less_than", ...$(t), value: e, inclusive: true });
}
function Xr(e, t) {
  return new rp({ check: "greater_than", ...$(t), value: e, inclusive: false });
}
function Et(e, t) {
  return new rp({ check: "greater_than", ...$(t), value: e, inclusive: true });
}
function tv(e) {
  return Xr(0, e);
}
function rv(e) {
  return Jr(0, e);
}
function nv(e) {
  return Jt(0, e);
}
function ov(e) {
  return Et(0, e);
}
function Ao(e, t) {
  return new jb({ check: "multiple_of", ...$(t), value: e });
}
function rs(e, t) {
  return new Lb({ check: "max_size", ...$(t), maximum: e });
}
function Co(e, t) {
  return new Fb({ check: "min_size", ...$(t), minimum: e });
}
function Qc(e, t) {
  return new Bb({ check: "size_equals", ...$(t), size: e });
}
function ns(e, t) {
  return new Hb({ check: "max_length", ...$(t), maximum: e });
}
function Cn(e, t) {
  return new qb({ check: "min_length", ...$(t), minimum: e });
}
function os(e, t) {
  return new Zb({ check: "length_equals", ...$(t), length: e });
}
function el(e, t) {
  return new Vb({ check: "string_format", format: "regex", ...$(t), pattern: e });
}
function tl(e) {
  return new Wb({ check: "string_format", format: "lowercase", ...$(e) });
}
function rl(e) {
  return new Kb({ check: "string_format", format: "uppercase", ...$(e) });
}
function nl(e, t) {
  return new Gb({ check: "string_format", format: "includes", ...$(t), includes: e });
}
function ol(e, t) {
  return new Jb({ check: "string_format", format: "starts_with", ...$(t), prefix: e });
}
function il(e, t) {
  return new Xb({ check: "string_format", format: "ends_with", ...$(t), suffix: e });
}
function iv(e, t, r) {
  return new Yb({ check: "property", property: e, schema: t, ...$(r) });
}
function sl(e, t) {
  return new Qb({ check: "mime_type", mime: e, ...$(t) });
}
function Yr(e) {
  return new e_({ check: "overwrite", tx: e });
}
function al(e) {
  return Yr((t) => t.normalize(e));
}
function cl() {
  return Yr((e) => e.trim());
}
function ll() {
  return Yr((e) => e.toLowerCase());
}
function ul() {
  return Yr((e) => e.toUpperCase());
}
function dl(e, t, r) {
  return new e({ type: "array", element: t, ...$(r) });
}
function ZV(e, t, r) {
  return new e({ type: "union", options: t, ...$(r) });
}
function VV(e, t, r, o) {
  return new e({ type: "union", options: r, discriminator: t, ...$(o) });
}
function WV(e, t, r) {
  return new e({ type: "intersection", left: t, right: r });
}
function sv(e, t, r, o) {
  let n = r instanceof G;
  return new e({ type: "tuple", items: t, rest: n ? r : null, ...$(n ? o : r) });
}
function KV(e, t, r, o) {
  return new e({ type: "record", keyType: t, valueType: r, ...$(o) });
}
function GV(e, t, r, o) {
  return new e({ type: "map", keyType: t, valueType: r, ...$(o) });
}
function JV(e, t, r) {
  return new e({ type: "set", valueType: t, ...$(r) });
}
function XV(e, t, r) {
  let o = Array.isArray(t) ? Object.fromEntries(t.map((n) => [n, n])) : t;
  return new e({ type: "enum", entries: o, ...$(r) });
}
function YV(e, t, r) {
  return new e({ type: "enum", entries: t, ...$(r) });
}
function QV(e, t, r) {
  return new e({ type: "literal", values: Array.isArray(t) ? t : [t], ...$(r) });
}
function Ef(e, t) {
  return new e({ type: "file", ...$(t) });
}
function eW(e, t) {
  return new e({ type: "transform", transform: t });
}
function tW(e, t) {
  return new e({ type: "optional", innerType: t });
}
function rW(e, t) {
  return new e({ type: "nullable", innerType: t });
}
function nW(e, t, r) {
  return new e({ type: "default", innerType: t, get defaultValue() {
    return typeof r === "function" ? r() : r;
  } });
}
function oW(e, t, r) {
  return new e({ type: "nonoptional", innerType: t, ...$(r) });
}
function iW(e, t) {
  return new e({ type: "success", innerType: t });
}
function sW(e, t, r) {
  return new e({ type: "catch", innerType: t, catchValue: typeof r === "function" ? r : () => r });
}
function aW(e, t, r) {
  return new e({ type: "pipe", in: t, out: r });
}
function cW(e, t) {
  return new e({ type: "readonly", innerType: t });
}
function lW(e, t, r) {
  return new e({ type: "template_literal", parts: t, ...$(r) });
}
function uW(e, t) {
  return new e({ type: "lazy", getter: t });
}
function dW(e, t) {
  return new e({ type: "promise", innerType: t });
}
function Tf(e, t, r) {
  let o = $(r);
  return o.abort ?? (o.abort = true), new e({ type: "custom", check: "custom", fn: t, ...o });
}
function Pf(e, t, r) {
  return new e({ type: "custom", check: "custom", fn: t, ...$(r) });
}
function If(e, t) {
  let r = $(t), o = r.truthy ?? ["true", "1", "yes", "on", "y", "enabled"], n = r.falsy ?? ["false", "0", "no", "off", "n", "disabled"];
  if (r.case !== "sensitive") o = o.map((g) => typeof g === "string" ? g.toLowerCase() : g), n = n.map((g) => typeof g === "string" ? g.toLowerCase() : g);
  let i = new Set(o), s = new Set(n), a = e.Pipe ?? Qi, c = e.Boolean ?? Ji, u = e.String ?? On, p = new (e.Transform ?? Yi)({ type: "transform", transform: (g, h) => {
    let y = g;
    if (r.case !== "sensitive") y = y.toLowerCase();
    if (i.has(y)) return true;
    else if (s.has(y)) return false;
    else return h.issues.push({ code: "invalid_value", expected: "stringbool", values: [...i, ...s], input: h.value, inst: p }), {};
  }, error: r.error }), f = new a({ type: "pipe", in: new u({ type: "string", error: r.error }), out: p, error: r.error });
  return new a({ type: "pipe", in: f, out: new c({ type: "boolean", error: r.error }), error: r.error });
}
function Rf(e, t, r, o = {}) {
  let n = $(o), i = { ...$(o), check: "string_format", type: "string", format: t, fn: typeof r === "function" ? r : (a) => r.test(a), ...n };
  if (r instanceof RegExp) i.pattern = r;
  return new e(i);
}
var av = class {
  constructor(e) {
    this._def = e, this.def = e;
  }
  implement(e) {
    if (typeof e !== "function") throw Error("implement() must be called with a function");
    let t = (...r) => {
      let o = this._def.input ? Po(this._def.input, r, void 0, { callee: t }) : r;
      if (!Array.isArray(o)) throw Error("Invalid arguments schema: not an array or tuple schema.");
      let n = e(...o);
      return this._def.output ? Po(this._def.output, n, void 0, { callee: t }) : n;
    };
    return t;
  }
  implementAsync(e) {
    if (typeof e !== "function") throw Error("implement() must be called with a function");
    let t = async (...r) => {
      let o = this._def.input ? await Io(this._def.input, r, void 0, { callee: t }) : r;
      if (!Array.isArray(o)) throw Error("Invalid arguments schema: not an array or tuple schema.");
      let n = await e(...o);
      return this._def.output ? Io(this._def.output, n, void 0, { callee: t }) : n;
    };
    return t;
  }
  input(...e) {
    let t = this.constructor;
    if (Array.isArray(e[0])) return new t({ type: "function", input: new An({ type: "tuple", items: e[0], rest: e[1] }), output: this._def.output });
    return new t({ type: "function", input: e[0], output: this._def.output });
  }
  output(e) {
    return new this.constructor({ type: "function", input: this._def.input, output: e });
  }
};
function $f(e) {
  return new av({ type: "function", input: Array.isArray(e?.input) ? sv(An, e?.input) : e?.input ?? dl(Xi, Oo($o)), output: e?.output ?? Oo($o) });
}
var Of = class {
  constructor(e) {
    this.counter = 0, this.metadataRegistry = e?.metadata ?? kt, this.target = e?.target ?? "draft-2020-12", this.unrepresentable = e?.unrepresentable ?? "throw", this.override = e?.override ?? (() => {
    }), this.io = e?.io ?? "output", this.seen = /* @__PURE__ */ new Map();
  }
  process(e, t = { path: [], schemaPath: [] }) {
    var r;
    let o = e._zod.def, n = { guid: "uuid", url: "uri", datetime: "date-time", json_string: "json-string", regex: "" }, i = this.seen.get(e);
    if (i) {
      if (i.count++, t.schemaPath.includes(e)) i.cycle = t.path;
      return i.schema;
    }
    let s = { schema: {}, count: 1, cycle: void 0, path: t.path };
    this.seen.set(e, s);
    let a = e._zod.toJSONSchema?.();
    if (a) s.schema = a;
    else {
      let d = { ...t, schemaPath: [...t.schemaPath, e], path: t.path }, p = e._zod.parent;
      if (p) s.ref = p, this.process(p, d), this.seen.get(p).isParent = true;
      else {
        let f = s.schema;
        switch (o.type) {
          case "string": {
            let m = f;
            m.type = "string";
            let { minimum: g, maximum: h, format: y, patterns: v, contentEncoding: x } = e._zod.bag;
            if (typeof g === "number") m.minLength = g;
            if (typeof h === "number") m.maxLength = h;
            if (y) {
              if (m.format = n[y] ?? y, m.format === "") delete m.format;
            }
            if (x) m.contentEncoding = x;
            if (v && v.size > 0) {
              let w = [...v];
              if (w.length === 1) m.pattern = w[0].source;
              else if (w.length > 1) s.schema.allOf = [...w.map((A) => ({ ...this.target === "draft-7" ? { type: "string" } : {}, pattern: A.source }))];
            }
            break;
          }
          case "number": {
            let m = f, { minimum: g, maximum: h, format: y, multipleOf: v, exclusiveMaximum: x, exclusiveMinimum: w } = e._zod.bag;
            if (typeof y === "string" && y.includes("int")) m.type = "integer";
            else m.type = "number";
            if (typeof w === "number") m.exclusiveMinimum = w;
            if (typeof g === "number") {
              if (m.minimum = g, typeof w === "number") if (w >= g) delete m.minimum;
              else delete m.exclusiveMinimum;
            }
            if (typeof x === "number") m.exclusiveMaximum = x;
            if (typeof h === "number") {
              if (m.maximum = h, typeof x === "number") if (x <= h) delete m.maximum;
              else delete m.exclusiveMaximum;
            }
            if (typeof v === "number") m.multipleOf = v;
            break;
          }
          case "boolean": {
            let m = f;
            m.type = "boolean";
            break;
          }
          case "bigint": {
            if (this.unrepresentable === "throw") throw Error("BigInt cannot be represented in JSON Schema");
            break;
          }
          case "symbol": {
            if (this.unrepresentable === "throw") throw Error("Symbols cannot be represented in JSON Schema");
            break;
          }
          case "null": {
            f.type = "null";
            break;
          }
          case "any":
            break;
          case "unknown":
            break;
          case "undefined":
          case "never": {
            f.not = {};
            break;
          }
          case "void": {
            if (this.unrepresentable === "throw") throw Error("Void cannot be represented in JSON Schema");
            break;
          }
          case "date": {
            if (this.unrepresentable === "throw") throw Error("Date cannot be represented in JSON Schema");
            break;
          }
          case "array": {
            let m = f, { minimum: g, maximum: h } = e._zod.bag;
            if (typeof g === "number") m.minItems = g;
            if (typeof h === "number") m.maxItems = h;
            m.type = "array", m.items = this.process(o.element, { ...d, path: [...d.path, "items"] });
            break;
          }
          case "object": {
            let m = f;
            m.type = "object", m.properties = {};
            let g = o.shape;
            for (let v in g) m.properties[v] = this.process(g[v], { ...d, path: [...d.path, "properties", v] });
            let h = new Set(Object.keys(g)), y = new Set([...h].filter((v) => {
              let x = o.shape[v]._zod;
              if (this.io === "input") return x.optin === void 0;
              else return x.optout === void 0;
            }));
            if (y.size > 0) m.required = Array.from(y);
            if (o.catchall?._zod.def.type === "never") m.additionalProperties = false;
            else if (!o.catchall) {
              if (this.io === "output") m.additionalProperties = false;
            } else if (o.catchall) m.additionalProperties = this.process(o.catchall, { ...d, path: [...d.path, "additionalProperties"] });
            break;
          }
          case "union": {
            let m = f;
            m.anyOf = o.options.map((g, h) => this.process(g, { ...d, path: [...d.path, "anyOf", h] }));
            break;
          }
          case "intersection": {
            let m = f, g = this.process(o.left, { ...d, path: [...d.path, "allOf", 0] }), h = this.process(o.right, { ...d, path: [...d.path, "allOf", 1] }), y = (x) => "allOf" in x && Object.keys(x).length === 1, v = [...y(g) ? g.allOf : [g], ...y(h) ? h.allOf : [h]];
            m.allOf = v;
            break;
          }
          case "tuple": {
            let m = f;
            m.type = "array";
            let g = o.items.map((v, x) => this.process(v, { ...d, path: [...d.path, "prefixItems", x] }));
            if (this.target === "draft-2020-12") m.prefixItems = g;
            else m.items = g;
            if (o.rest) {
              let v = this.process(o.rest, { ...d, path: [...d.path, "items"] });
              if (this.target === "draft-2020-12") m.items = v;
              else m.additionalItems = v;
            }
            if (o.rest) m.items = this.process(o.rest, { ...d, path: [...d.path, "items"] });
            let { minimum: h, maximum: y } = e._zod.bag;
            if (typeof h === "number") m.minItems = h;
            if (typeof y === "number") m.maxItems = y;
            break;
          }
          case "record": {
            let m = f;
            m.type = "object", m.propertyNames = this.process(o.keyType, { ...d, path: [...d.path, "propertyNames"] }), m.additionalProperties = this.process(o.valueType, { ...d, path: [...d.path, "additionalProperties"] });
            break;
          }
          case "map": {
            if (this.unrepresentable === "throw") throw Error("Map cannot be represented in JSON Schema");
            break;
          }
          case "set": {
            if (this.unrepresentable === "throw") throw Error("Set cannot be represented in JSON Schema");
            break;
          }
          case "enum": {
            let m = f, g = bc(o.entries);
            if (g.every((h) => typeof h === "number")) m.type = "number";
            if (g.every((h) => typeof h === "string")) m.type = "string";
            m.enum = g;
            break;
          }
          case "literal": {
            let m = f, g = [];
            for (let h of o.values) if (h === void 0) {
              if (this.unrepresentable === "throw") throw Error("Literal `undefined` cannot be represented in JSON Schema");
            } else if (typeof h === "bigint") if (this.unrepresentable === "throw") throw Error("BigInt literals cannot be represented in JSON Schema");
            else g.push(Number(h));
            else g.push(h);
            if (g.length === 0) ;
            else if (g.length === 1) {
              let h = g[0];
              m.type = h === null ? "null" : typeof h, m.const = h;
            } else {
              if (g.every((h) => typeof h === "number")) m.type = "number";
              if (g.every((h) => typeof h === "string")) m.type = "string";
              if (g.every((h) => typeof h === "boolean")) m.type = "string";
              if (g.every((h) => h === null)) m.type = "null";
              m.enum = g;
            }
            break;
          }
          case "file": {
            let m = f, g = { type: "string", format: "binary", contentEncoding: "binary" }, { minimum: h, maximum: y, mime: v } = e._zod.bag;
            if (h !== void 0) g.minLength = h;
            if (y !== void 0) g.maxLength = y;
            if (v) if (v.length === 1) g.contentMediaType = v[0], Object.assign(m, g);
            else m.anyOf = v.map((x) => ({ ...g, contentMediaType: x }));
            else Object.assign(m, g);
            break;
          }
          case "transform": {
            if (this.unrepresentable === "throw") throw Error("Transforms cannot be represented in JSON Schema");
            break;
          }
          case "nullable": {
            let m = this.process(o.innerType, d);
            f.anyOf = [m, { type: "null" }];
            break;
          }
          case "nonoptional": {
            this.process(o.innerType, d), s.ref = o.innerType;
            break;
          }
          case "success": {
            let m = f;
            m.type = "boolean";
            break;
          }
          case "default": {
            this.process(o.innerType, d), s.ref = o.innerType, f.default = JSON.parse(JSON.stringify(o.defaultValue));
            break;
          }
          case "prefault": {
            if (this.process(o.innerType, d), s.ref = o.innerType, this.io === "input") f._prefault = JSON.parse(JSON.stringify(o.defaultValue));
            break;
          }
          case "catch": {
            this.process(o.innerType, d), s.ref = o.innerType;
            let m;
            try {
              m = o.catchValue(void 0);
            } catch {
              throw Error("Dynamic catch values are not supported in JSON Schema");
            }
            f.default = m;
            break;
          }
          case "nan": {
            if (this.unrepresentable === "throw") throw Error("NaN cannot be represented in JSON Schema");
            break;
          }
          case "template_literal": {
            let m = f, g = e._zod.pattern;
            if (!g) throw Error("Pattern not found in template literal");
            m.type = "string", m.pattern = g.source;
            break;
          }
          case "pipe": {
            let m = this.io === "input" ? o.in._zod.def.type === "transform" ? o.out : o.in : o.out;
            this.process(m, d), s.ref = m;
            break;
          }
          case "readonly": {
            this.process(o.innerType, d), s.ref = o.innerType, f.readOnly = true;
            break;
          }
          case "promise": {
            this.process(o.innerType, d), s.ref = o.innerType;
            break;
          }
          case "optional": {
            this.process(o.innerType, d), s.ref = o.innerType;
            break;
          }
          case "lazy": {
            let m = e._zod.innerType;
            this.process(m, d), s.ref = m;
            break;
          }
          case "custom": {
            if (this.unrepresentable === "throw") throw Error("Custom types cannot be represented in JSON Schema");
            break;
          }
          default:
        }
      }
    }
    let c = this.metadataRegistry.get(e);
    if (c) Object.assign(s.schema, c);
    if (this.io === "input" && Je(e)) delete s.schema.examples, delete s.schema.default;
    if (this.io === "input" && s.schema._prefault) (r = s.schema).default ?? (r.default = s.schema._prefault);
    return delete s.schema._prefault, this.seen.get(e).schema;
  }
  emit(e, t) {
    let r = { cycles: t?.cycles ?? "ref", reused: t?.reused ?? "inline", external: t?.external ?? void 0 }, o = this.seen.get(e);
    if (!o) throw Error("Unprocessed schema. This is a bug in Zod.");
    let n = (u) => {
      let d = this.target === "draft-2020-12" ? "$defs" : "definitions";
      if (r.external) {
        let g = r.external.registry.get(u[0])?.id;
        if (g) return { ref: r.external.uri(g) };
        let h = u[1].defId ?? u[1].schema.id ?? `schema${this.counter++}`;
        return u[1].defId = h, { defId: h, ref: `${r.external.uri("__shared")}#/${d}/${h}` };
      }
      if (u[1] === o) return { ref: "#" };
      let f = `${"#"}/${d}/`, m = u[1].schema.id ?? `__schema${this.counter++}`;
      return { defId: m, ref: f + m };
    }, i = (u) => {
      if (u[1].schema.$ref) return;
      let d = u[1], { ref: p, defId: f } = n(u);
      if (d.def = { ...d.schema }, f) d.defId = f;
      let m = d.schema;
      for (let g in m) delete m[g];
      m.$ref = p;
    };
    for (let u of this.seen.entries()) {
      let d = u[1];
      if (e === u[0]) {
        i(u);
        continue;
      }
      if (r.external) {
        let f = r.external.registry.get(u[0])?.id;
        if (e !== u[0] && f) {
          i(u);
          continue;
        }
      }
      if (this.metadataRegistry.get(u[0])?.id) {
        i(u);
        continue;
      }
      if (d.cycle) {
        if (r.cycles === "throw") throw Error(`Cycle detected: #/${d.cycle?.join("/")}/<root>

Set the \`cycles\` parameter to \`"ref"\` to resolve cyclical schemas with defs.`);
        else if (r.cycles === "ref") i(u);
        continue;
      }
      if (d.count > 1) {
        if (r.reused === "ref") {
          i(u);
          continue;
        }
      }
    }
    let s = (u, d) => {
      let p = this.seen.get(u), f = p.def ?? p.schema, m = { ...f };
      if (p.ref === null) return;
      let g = p.ref;
      if (p.ref = null, g) {
        s(g, d);
        let h = this.seen.get(g).schema;
        if (h.$ref && d.target === "draft-7") f.allOf = f.allOf ?? [], f.allOf.push(h);
        else Object.assign(f, h), Object.assign(f, m);
      }
      if (!p.isParent) this.override({ zodSchema: u, jsonSchema: f, path: p.path ?? [] });
    };
    for (let u of [...this.seen.entries()].reverse()) s(u[0], { target: this.target });
    let a = {};
    if (this.target === "draft-2020-12") a.$schema = "https://json-schema.org/draft/2020-12/schema";
    else if (this.target === "draft-7") a.$schema = "http://json-schema.org/draft-07/schema#";
    else console.warn(`Invalid target: ${this.target}`);
    Object.assign(a, o.def);
    let c = r.external?.defs ?? {};
    for (let u of this.seen.entries()) {
      let d = u[1];
      if (d.def && d.defId) c[d.defId] = d.def;
    }
    if (!r.external && Object.keys(c).length > 0) if (this.target === "draft-2020-12") a.$defs = c;
    else a.definitions = c;
    try {
      return JSON.parse(JSON.stringify(a));
    } catch (u) {
      throw Error("Error converting schema to JSON.");
    }
  }
};
function is(e, t) {
  if (e instanceof $c) {
    let o = new Of(t), n = {};
    for (let a of e._idmap.entries()) {
      let [c, u] = a;
      o.process(u);
    }
    let i = {}, s = { registry: e, uri: t?.uri || ((a) => a), defs: n };
    for (let a of e._idmap.entries()) {
      let [c, u] = a;
      i[c] = o.emit(u, { ...t, external: s });
    }
    if (Object.keys(n).length > 0) {
      let a = o.target === "draft-2020-12" ? "$defs" : "definitions";
      i.__shared = { [a]: n };
    }
    return { schemas: i };
  }
  let r = new Of(t);
  return r.process(e), r.emit(e, t);
}
function Je(e, t) {
  let r = t ?? { seen: /* @__PURE__ */ new Set() };
  if (r.seen.has(e)) return false;
  r.seen.add(e);
  let n = e._zod.def;
  switch (n.type) {
    case "string":
    case "number":
    case "bigint":
    case "boolean":
    case "date":
    case "symbol":
    case "undefined":
    case "null":
    case "any":
    case "unknown":
    case "never":
    case "void":
    case "literal":
    case "enum":
    case "nan":
    case "file":
    case "template_literal":
      return false;
    case "array":
      return Je(n.element, r);
    case "object": {
      for (let i in n.shape) if (Je(n.shape[i], r)) return true;
      return false;
    }
    case "union": {
      for (let i of n.options) if (Je(i, r)) return true;
      return false;
    }
    case "intersection":
      return Je(n.left, r) || Je(n.right, r);
    case "tuple": {
      for (let i of n.items) if (Je(i, r)) return true;
      if (n.rest && Je(n.rest, r)) return true;
      return false;
    }
    case "record":
      return Je(n.keyType, r) || Je(n.valueType, r);
    case "map":
      return Je(n.keyType, r) || Je(n.valueType, r);
    case "set":
      return Je(n.valueType, r);
    case "promise":
    case "optional":
    case "nonoptional":
    case "nullable":
    case "readonly":
      return Je(n.innerType, r);
    case "lazy":
      return Je(n.getter(), r);
    case "default":
      return Je(n.innerType, r);
    case "prefault":
      return Je(n.innerType, r);
    case "custom":
      return false;
    case "transform":
      return true;
    case "pipe":
      return Je(n.in, r) || Je(n.out, r);
    case "success":
      return false;
    case "catch":
      return false;
    default:
  }
  throw Error(`Unknown schema type: ${n.type}`);
}
var Y0 = {};
var fW = b("ZodMiniType", (e, t) => {
  if (!e._zod) throw Error("Uninitialized schema in ZodMiniType.");
  G.init(e, t), e.def = t, e.parse = (r, o) => Po(e, r, o, { callee: e.parse }), e.safeParse = (r, o) => In(e, r, o), e.parseAsync = async (r, o) => Io(e, r, o, { callee: e.parseAsync }), e.safeParseAsync = async (r, o) => Rn(e, r, o), e.check = (...r) => e.clone({ ...t, checks: [...t.checks ?? [], ...r.map((o) => typeof o === "function" ? { _zod: { check: o, def: { check: "custom" }, onattach: [] } } : o)] }), e.clone = (r, o) => at(e, r, o), e.brand = () => e, e.register = (r, o) => (r.add(e, o), e);
});
var mW = b("ZodMiniObject", (e, t) => {
  Pc.init(e, t), fW.init(e, t), O.defineLazy(e, "shape", () => t.shape);
});
var l = {};
xr(l, { xid: () => OW, void: () => YW, uuidv7: () => kW, uuidv6: () => wW, uuidv4: () => xW, uuid: () => SW, url: () => EW, uppercase: () => rl, unknown: () => Oe, union: () => we, undefined: () => JW, ulid: () => $W, uint64: () => KW, uint32: () => ZW, tuple: () => rK, trim: () => cl, treeifyError: () => Kd, transform: () => Lv, toUpperCase: () => ul, toLowerCase: () => ll, toJSONSchema: () => is, templateLiteral: () => dK, symbol: () => GW, superRefine: () => j$, success: () => lK, stringbool: () => mK, stringFormat: () => FW, string: () => S, strictObject: () => tK, startsWith: () => ol, size: () => Qc, setErrorMap: () => yK, set: () => iK, safeParseAsync: () => hv, safeParse: () => gv, registry: () => Oc, regexes: () => $n, regex: () => el, refine: () => N$, record: () => ke, readonly: () => $$, property: () => iv, promise: () => pK, prettifyError: () => Gd, preprocess: () => Kf, prefault: () => w$, positive: () => tv, pipe: () => Ff, partialRecord: () => nK, parseAsync: () => mv, parse: () => fv, overwrite: () => Yr, optional: () => Re, object: () => L, number: () => fe, nullish: () => cK, nullable: () => Lf, null: () => Bf, normalize: () => al, nonpositive: () => nv, nonoptional: () => k$, nonnegative: () => ov, never: () => Hf, negative: () => rv, nativeEnum: () => sK, nanoid: () => PW, nan: () => uK, multipleOf: () => Ao, minSize: () => Co, minLength: () => Cn, mime: () => sl, maxSize: () => rs, maxLength: () => ns, map: () => oK, lte: () => Jt, lt: () => Jr, lowercase: () => tl, looseObject: () => ct, locales: () => es, literal: () => B, length: () => os, lazy: () => C$, ksuid: () => AW, keyof: () => eK, jwt: () => LW, json: () => gK, iso: () => as, ipv6: () => MW, ipv4: () => CW, intersection: () => yl, int64: () => WW, int32: () => qW, int: () => yv, instanceof: () => fK, includes: () => nl, guid: () => vW, gte: () => Et, gt: () => Xr, globalRegistry: () => kt, getErrorMap: () => bK, function: () => $f, formatError: () => Ki, float64: () => HW, float32: () => BW, flattenError: () => Wi, file: () => aK, enum: () => ft, endsWith: () => il, emoji: () => TW, email: () => _W, e164: () => zW, discriminatedUnion: () => Vf, date: () => QW, custom: () => qv, cuid2: () => RW, cuid: () => IW, core: () => ur, config: () => He, coerce: () => Zv, clone: () => at, cidrv6: () => NW, cidrv4: () => DW, check: () => D$, catch: () => P$, boolean: () => Ve, bigint: () => VW, base64url: () => UW, base64: () => jW, array: () => ie, any: () => XW, _default: () => S$, _ZodString: () => bv, ZodXID: () => Tv, ZodVoid: () => u$, ZodUnknown: () => c$, ZodUnion: () => jv, ZodUndefined: () => i$, ZodUUID: () => Qr, ZodURL: () => vv, ZodULID: () => Ev, ZodType: () => ne, ZodTuple: () => m$, ZodTransform: () => zv, ZodTemplateLiteral: () => O$, ZodSymbol: () => o$, ZodSuccess: () => E$, ZodStringFormat: () => Ie, ZodString: () => fl, ZodSet: () => h$, ZodRecord: () => Uv, ZodRealError: () => cs, ZodReadonly: () => R$, ZodPromise: () => M$, ZodPrefault: () => x$, ZodPipe: () => Hv, ZodOptional: () => Fv, ZodObject: () => Zf, ZodNumberFormat: () => ls, ZodNumber: () => ml, ZodNullable: () => _$, ZodNull: () => s$, ZodNonOptional: () => Bv, ZodNever: () => l$, ZodNanoID: () => xv, ZodNaN: () => I$, ZodMap: () => g$, ZodLiteral: () => y$, ZodLazy: () => A$, ZodKSUID: () => Pv, ZodJWT: () => Dv, ZodIssueCode: () => hK, ZodIntersection: () => f$, ZodISOTime: () => jf, ZodISODuration: () => Uf, ZodISODateTime: () => Df, ZodISODate: () => Nf, ZodIPv6: () => Rv, ZodIPv4: () => Iv, ZodGUID: () => zf, ZodFile: () => b$, ZodError: () => yW, ZodEnum: () => pl, ZodEmoji: () => Sv, ZodEmail: () => _v, ZodE164: () => Mv, ZodDiscriminatedUnion: () => p$, ZodDefault: () => v$, ZodDate: () => qf, ZodCustomStringFormat: () => n$, ZodCustom: () => Wf, ZodCatch: () => T$, ZodCUID2: () => kv, ZodCUID: () => wv, ZodCIDRv6: () => Ov, ZodCIDRv4: () => $v, ZodBoolean: () => gl, ZodBigIntFormat: () => Nv, ZodBigInt: () => hl, ZodBase64URL: () => Cv, ZodBase64: () => Av, ZodArray: () => d$, ZodAny: () => a$, TimePrecision: () => sf, NEVER: () => Zd, $output: () => rf, $input: () => nf, $brand: () => Vd });
var as = {};
xr(as, { time: () => dv, duration: () => pv, datetime: () => lv, date: () => uv, ZodISOTime: () => jf, ZodISODuration: () => Uf, ZodISODateTime: () => Df, ZodISODate: () => Nf });
var Df = b("ZodISODateTime", (e, t) => {
  n_.init(e, t), Ie.init(e, t);
});
function lv(e) {
  return W_(Df, e);
}
var Nf = b("ZodISODate", (e, t) => {
  o_.init(e, t), Ie.init(e, t);
});
function uv(e) {
  return K_(Nf, e);
}
var jf = b("ZodISOTime", (e, t) => {
  i_.init(e, t), Ie.init(e, t);
});
function dv(e) {
  return G_(jf, e);
}
var Uf = b("ZodISODuration", (e, t) => {
  s_.init(e, t), Ie.init(e, t);
});
function pv(e) {
  return J_(Uf, e);
}
var r$ = (e, t) => {
  kc.init(e, t), e.name = "ZodError", Object.defineProperties(e, { format: { value: (r) => Ki(e, r) }, flatten: { value: (r) => Wi(e, r) }, addIssue: { value: (r) => e.issues.push(r) }, addIssues: { value: (r) => e.issues.push(...r) }, isEmpty: { get() {
    return e.issues.length === 0;
  } } });
};
var yW = b("ZodError", r$);
var cs = b("ZodError", r$, { Parent: Error });
var fv = Jd(cs);
var mv = Xd(cs);
var gv = Yd(cs);
var hv = Qd(cs);
var ne = b("ZodType", (e, t) => (G.init(e, t), e.def = t, Object.defineProperty(e, "_def", { value: t }), e.check = (...r) => e.clone({ ...t, checks: [...t.checks ?? [], ...r.map((o) => typeof o === "function" ? { _zod: { check: o, def: { check: "custom" }, onattach: [] } } : o)] }), e.clone = (r, o) => at(e, r, o), e.brand = () => e, e.register = (r, o) => (r.add(e, o), e), e.parse = (r, o) => fv(e, r, o, { callee: e.parse }), e.safeParse = (r, o) => gv(e, r, o), e.parseAsync = async (r, o) => mv(e, r, o, { callee: e.parseAsync }), e.safeParseAsync = async (r, o) => hv(e, r, o), e.spa = e.safeParseAsync, e.refine = (r, o) => e.check(N$(r, o)), e.superRefine = (r) => e.check(j$(r)), e.overwrite = (r) => e.check(Yr(r)), e.optional = () => Re(e), e.nullable = () => Lf(e), e.nullish = () => Re(Lf(e)), e.nonoptional = (r) => k$(e, r), e.array = () => ie(e), e.or = (r) => we([e, r]), e.and = (r) => yl(e, r), e.transform = (r) => Ff(e, Lv(r)), e.default = (r) => S$(e, r), e.prefault = (r) => w$(e, r), e.catch = (r) => P$(e, r), e.pipe = (r) => Ff(e, r), e.readonly = () => $$(e), e.describe = (r) => {
  let o = e.clone();
  return kt.add(o, { description: r }), o;
}, Object.defineProperty(e, "description", { get() {
  return kt.get(e)?.description;
}, configurable: true }), e.meta = (...r) => {
  if (r.length === 0) return kt.get(e);
  let o = e.clone();
  return kt.add(o, r[0]), o;
}, e.isOptional = () => e.safeParse(void 0).success, e.isNullable = () => e.safeParse(null).success, e));
var bv = b("_ZodString", (e, t) => {
  On.init(e, t), ne.init(e, t);
  let r = e._zod.bag;
  e.format = r.format ?? null, e.minLength = r.minimum ?? null, e.maxLength = r.maximum ?? null, e.regex = (...o) => e.check(el(...o)), e.includes = (...o) => e.check(nl(...o)), e.startsWith = (...o) => e.check(ol(...o)), e.endsWith = (...o) => e.check(il(...o)), e.min = (...o) => e.check(Cn(...o)), e.max = (...o) => e.check(ns(...o)), e.length = (...o) => e.check(os(...o)), e.nonempty = (...o) => e.check(Cn(1, ...o)), e.lowercase = (o) => e.check(tl(o)), e.uppercase = (o) => e.check(rl(o)), e.trim = () => e.check(cl()), e.normalize = (...o) => e.check(al(...o)), e.toLowerCase = () => e.check(ll()), e.toUpperCase = () => e.check(ul());
});
var fl = b("ZodString", (e, t) => {
  On.init(e, t), bv.init(e, t), e.email = (r) => e.check(Ac(_v, r)), e.url = (r) => e.check(jc(vv, r)), e.jwt = (r) => e.check(Yc(Dv, r)), e.emoji = (r) => e.check(Uc(Sv, r)), e.guid = (r) => e.check(ts(zf, r)), e.uuid = (r) => e.check(Cc(Qr, r)), e.uuidv4 = (r) => e.check(Mc(Qr, r)), e.uuidv6 = (r) => e.check(Dc(Qr, r)), e.uuidv7 = (r) => e.check(Nc(Qr, r)), e.nanoid = (r) => e.check(zc(xv, r)), e.guid = (r) => e.check(ts(zf, r)), e.cuid = (r) => e.check(Lc(wv, r)), e.cuid2 = (r) => e.check(Fc(kv, r)), e.ulid = (r) => e.check(Bc(Ev, r)), e.base64 = (r) => e.check(Gc(Av, r)), e.base64url = (r) => e.check(Jc(Cv, r)), e.xid = (r) => e.check(Hc(Tv, r)), e.ksuid = (r) => e.check(qc(Pv, r)), e.ipv4 = (r) => e.check(Zc(Iv, r)), e.ipv6 = (r) => e.check(Vc(Rv, r)), e.cidrv4 = (r) => e.check(Wc($v, r)), e.cidrv6 = (r) => e.check(Kc(Ov, r)), e.e164 = (r) => e.check(Xc(Mv, r)), e.datetime = (r) => e.check(lv(r)), e.date = (r) => e.check(uv(r)), e.time = (r) => e.check(dv(r)), e.duration = (r) => e.check(pv(r));
});
function S(e) {
  return of(fl, e);
}
var Ie = b("ZodStringFormat", (e, t) => {
  xe.init(e, t), bv.init(e, t);
});
var _v = b("ZodEmail", (e, t) => {
  cp.init(e, t), Ie.init(e, t);
});
function _W(e) {
  return Ac(_v, e);
}
var zf = b("ZodGUID", (e, t) => {
  sp.init(e, t), Ie.init(e, t);
});
function vW(e) {
  return ts(zf, e);
}
var Qr = b("ZodUUID", (e, t) => {
  ap.init(e, t), Ie.init(e, t);
});
function SW(e) {
  return Cc(Qr, e);
}
function xW(e) {
  return Mc(Qr, e);
}
function wW(e) {
  return Dc(Qr, e);
}
function kW(e) {
  return Nc(Qr, e);
}
var vv = b("ZodURL", (e, t) => {
  lp.init(e, t), Ie.init(e, t);
});
function EW(e) {
  return jc(vv, e);
}
var Sv = b("ZodEmoji", (e, t) => {
  up.init(e, t), Ie.init(e, t);
});
function TW(e) {
  return Uc(Sv, e);
}
var xv = b("ZodNanoID", (e, t) => {
  dp.init(e, t), Ie.init(e, t);
});
function PW(e) {
  return zc(xv, e);
}
var wv = b("ZodCUID", (e, t) => {
  pp.init(e, t), Ie.init(e, t);
});
function IW(e) {
  return Lc(wv, e);
}
var kv = b("ZodCUID2", (e, t) => {
  fp.init(e, t), Ie.init(e, t);
});
function RW(e) {
  return Fc(kv, e);
}
var Ev = b("ZodULID", (e, t) => {
  mp.init(e, t), Ie.init(e, t);
});
function $W(e) {
  return Bc(Ev, e);
}
var Tv = b("ZodXID", (e, t) => {
  gp.init(e, t), Ie.init(e, t);
});
function OW(e) {
  return Hc(Tv, e);
}
var Pv = b("ZodKSUID", (e, t) => {
  hp.init(e, t), Ie.init(e, t);
});
function AW(e) {
  return qc(Pv, e);
}
var Iv = b("ZodIPv4", (e, t) => {
  yp.init(e, t), Ie.init(e, t);
});
function CW(e) {
  return Zc(Iv, e);
}
var Rv = b("ZodIPv6", (e, t) => {
  bp.init(e, t), Ie.init(e, t);
});
function MW(e) {
  return Vc(Rv, e);
}
var $v = b("ZodCIDRv4", (e, t) => {
  _p.init(e, t), Ie.init(e, t);
});
function DW(e) {
  return Wc($v, e);
}
var Ov = b("ZodCIDRv6", (e, t) => {
  vp.init(e, t), Ie.init(e, t);
});
function NW(e) {
  return Kc(Ov, e);
}
var Av = b("ZodBase64", (e, t) => {
  Sp.init(e, t), Ie.init(e, t);
});
function jW(e) {
  return Gc(Av, e);
}
var Cv = b("ZodBase64URL", (e, t) => {
  xp.init(e, t), Ie.init(e, t);
});
function UW(e) {
  return Jc(Cv, e);
}
var Mv = b("ZodE164", (e, t) => {
  wp.init(e, t), Ie.init(e, t);
});
function zW(e) {
  return Xc(Mv, e);
}
var Dv = b("ZodJWT", (e, t) => {
  kp.init(e, t), Ie.init(e, t);
});
function LW(e) {
  return Yc(Dv, e);
}
var n$ = b("ZodCustomStringFormat", (e, t) => {
  Ep.init(e, t), Ie.init(e, t);
});
function FW(e, t, r = {}) {
  return Rf(n$, e, t, r);
}
var ml = b("ZodNumber", (e, t) => {
  Ec.init(e, t), ne.init(e, t), e.gt = (o, n) => e.check(Xr(o, n)), e.gte = (o, n) => e.check(Et(o, n)), e.min = (o, n) => e.check(Et(o, n)), e.lt = (o, n) => e.check(Jr(o, n)), e.lte = (o, n) => e.check(Jt(o, n)), e.max = (o, n) => e.check(Jt(o, n)), e.int = (o) => e.check(yv(o)), e.safe = (o) => e.check(yv(o)), e.positive = (o) => e.check(Xr(0, o)), e.nonnegative = (o) => e.check(Et(0, o)), e.negative = (o) => e.check(Jr(0, o)), e.nonpositive = (o) => e.check(Jt(0, o)), e.multipleOf = (o, n) => e.check(Ao(o, n)), e.step = (o, n) => e.check(Ao(o, n)), e.finite = () => e;
  let r = e._zod.bag;
  e.minValue = Math.max(r.minimum ?? Number.NEGATIVE_INFINITY, r.exclusiveMinimum ?? Number.NEGATIVE_INFINITY) ?? null, e.maxValue = Math.min(r.maximum ?? Number.POSITIVE_INFINITY, r.exclusiveMaximum ?? Number.POSITIVE_INFINITY) ?? null, e.isInt = (r.format ?? "").includes("int") || Number.isSafeInteger(r.multipleOf ?? 0.5), e.isFinite = true, e.format = r.format ?? null;
});
function fe(e) {
  return af(ml, e);
}
var ls = b("ZodNumberFormat", (e, t) => {
  Tp.init(e, t), ml.init(e, t);
});
function yv(e) {
  return cf(ls, e);
}
function BW(e) {
  return lf(ls, e);
}
function HW(e) {
  return uf(ls, e);
}
function qW(e) {
  return df(ls, e);
}
function ZW(e) {
  return pf(ls, e);
}
var gl = b("ZodBoolean", (e, t) => {
  Ji.init(e, t), ne.init(e, t);
});
function Ve(e) {
  return ff(gl, e);
}
var hl = b("ZodBigInt", (e, t) => {
  Tc.init(e, t), ne.init(e, t), e.gte = (o, n) => e.check(Et(o, n)), e.min = (o, n) => e.check(Et(o, n)), e.gt = (o, n) => e.check(Xr(o, n)), e.gte = (o, n) => e.check(Et(o, n)), e.min = (o, n) => e.check(Et(o, n)), e.lt = (o, n) => e.check(Jr(o, n)), e.lte = (o, n) => e.check(Jt(o, n)), e.max = (o, n) => e.check(Jt(o, n)), e.positive = (o) => e.check(Xr(BigInt(0), o)), e.negative = (o) => e.check(Jr(BigInt(0), o)), e.nonpositive = (o) => e.check(Jt(BigInt(0), o)), e.nonnegative = (o) => e.check(Et(BigInt(0), o)), e.multipleOf = (o, n) => e.check(Ao(o, n));
  let r = e._zod.bag;
  e.minValue = r.minimum ?? null, e.maxValue = r.maximum ?? null, e.format = r.format ?? null;
});
function VW(e) {
  return mf(hl, e);
}
var Nv = b("ZodBigIntFormat", (e, t) => {
  Pp.init(e, t), hl.init(e, t);
});
function WW(e) {
  return gf(Nv, e);
}
function KW(e) {
  return hf(Nv, e);
}
var o$ = b("ZodSymbol", (e, t) => {
  Ip.init(e, t), ne.init(e, t);
});
function GW(e) {
  return yf(o$, e);
}
var i$ = b("ZodUndefined", (e, t) => {
  Rp.init(e, t), ne.init(e, t);
});
function JW(e) {
  return bf(i$, e);
}
var s$ = b("ZodNull", (e, t) => {
  $p.init(e, t), ne.init(e, t);
});
function Bf(e) {
  return _f(s$, e);
}
var a$ = b("ZodAny", (e, t) => {
  Op.init(e, t), ne.init(e, t);
});
function XW() {
  return vf(a$);
}
var c$ = b("ZodUnknown", (e, t) => {
  $o.init(e, t), ne.init(e, t);
});
function Oe() {
  return Oo(c$);
}
var l$ = b("ZodNever", (e, t) => {
  Ap.init(e, t), ne.init(e, t);
});
function Hf(e) {
  return Sf(l$, e);
}
var u$ = b("ZodVoid", (e, t) => {
  Cp.init(e, t), ne.init(e, t);
});
function YW(e) {
  return xf(u$, e);
}
var qf = b("ZodDate", (e, t) => {
  Mp.init(e, t), ne.init(e, t), e.min = (o, n) => e.check(Et(o, n)), e.max = (o, n) => e.check(Jt(o, n));
  let r = e._zod.bag;
  e.minDate = r.minimum ? new Date(r.minimum) : null, e.maxDate = r.maximum ? new Date(r.maximum) : null;
});
function QW(e) {
  return wf(qf, e);
}
var d$ = b("ZodArray", (e, t) => {
  Xi.init(e, t), ne.init(e, t), e.element = t.element, e.min = (r, o) => e.check(Cn(r, o)), e.nonempty = (r) => e.check(Cn(1, r)), e.max = (r, o) => e.check(ns(r, o)), e.length = (r, o) => e.check(os(r, o)), e.unwrap = () => e.element;
});
function ie(e, t) {
  return dl(d$, e, t);
}
function eK(e) {
  let t = e._zod.def.shape;
  return B(Object.keys(t));
}
var Zf = b("ZodObject", (e, t) => {
  Pc.init(e, t), ne.init(e, t), O.defineLazy(e, "shape", () => t.shape), e.keyof = () => ft(Object.keys(e._zod.def.shape)), e.catchall = (r) => e.clone({ ...e._zod.def, catchall: r }), e.passthrough = () => e.clone({ ...e._zod.def, catchall: Oe() }), e.loose = () => e.clone({ ...e._zod.def, catchall: Oe() }), e.strict = () => e.clone({ ...e._zod.def, catchall: Hf() }), e.strip = () => e.clone({ ...e._zod.def, catchall: void 0 }), e.extend = (r) => O.extend(e, r), e.merge = (r) => O.merge(e, r), e.pick = (r) => O.pick(e, r), e.omit = (r) => O.omit(e, r), e.partial = (...r) => O.partial(Fv, e, r[0]), e.required = (...r) => O.required(Bv, e, r[0]);
});
function L(e, t) {
  let r = { type: "object", get shape() {
    return O.assignProp(this, "shape", { ...e }), this.shape;
  }, ...O.normalizeParams(t) };
  return new Zf(r);
}
function tK(e, t) {
  return new Zf({ type: "object", get shape() {
    return O.assignProp(this, "shape", { ...e }), this.shape;
  }, catchall: Hf(), ...O.normalizeParams(t) });
}
function ct(e, t) {
  return new Zf({ type: "object", get shape() {
    return O.assignProp(this, "shape", { ...e }), this.shape;
  }, catchall: Oe(), ...O.normalizeParams(t) });
}
var jv = b("ZodUnion", (e, t) => {
  Ic.init(e, t), ne.init(e, t), e.options = t.options;
});
function we(e, t) {
  return new jv({ type: "union", options: e, ...O.normalizeParams(t) });
}
var p$ = b("ZodDiscriminatedUnion", (e, t) => {
  jv.init(e, t), Dp.init(e, t);
});
function Vf(e, t, r) {
  return new p$({ type: "union", options: t, discriminator: e, ...O.normalizeParams(r) });
}
var f$ = b("ZodIntersection", (e, t) => {
  Np.init(e, t), ne.init(e, t);
});
function yl(e, t) {
  return new f$({ type: "intersection", left: e, right: t });
}
var m$ = b("ZodTuple", (e, t) => {
  An.init(e, t), ne.init(e, t), e.rest = (r) => e.clone({ ...e._zod.def, rest: r });
});
function rK(e, t, r) {
  let o = t instanceof G, n = o ? r : t;
  return new m$({ type: "tuple", items: e, rest: o ? t : null, ...O.normalizeParams(n) });
}
var Uv = b("ZodRecord", (e, t) => {
  jp.init(e, t), ne.init(e, t), e.keyType = t.keyType, e.valueType = t.valueType;
});
function ke(e, t, r) {
  return new Uv({ type: "record", keyType: e, valueType: t, ...O.normalizeParams(r) });
}
function nK(e, t, r) {
  return new Uv({ type: "record", keyType: we([e, Hf()]), valueType: t, ...O.normalizeParams(r) });
}
var g$ = b("ZodMap", (e, t) => {
  Up.init(e, t), ne.init(e, t), e.keyType = t.keyType, e.valueType = t.valueType;
});
function oK(e, t, r) {
  return new g$({ type: "map", keyType: e, valueType: t, ...O.normalizeParams(r) });
}
var h$ = b("ZodSet", (e, t) => {
  zp.init(e, t), ne.init(e, t), e.min = (...r) => e.check(Co(...r)), e.nonempty = (r) => e.check(Co(1, r)), e.max = (...r) => e.check(rs(...r)), e.size = (...r) => e.check(Qc(...r));
});
function iK(e, t) {
  return new h$({ type: "set", valueType: e, ...O.normalizeParams(t) });
}
var pl = b("ZodEnum", (e, t) => {
  Lp.init(e, t), ne.init(e, t), e.enum = t.entries, e.options = Object.values(t.entries);
  let r = new Set(Object.keys(t.entries));
  e.extract = (o, n) => {
    let i = {};
    for (let s of o) if (r.has(s)) i[s] = t.entries[s];
    else throw Error(`Key ${s} not found in enum`);
    return new pl({ ...t, checks: [], ...O.normalizeParams(n), entries: i });
  }, e.exclude = (o, n) => {
    let i = { ...t.entries };
    for (let s of o) if (r.has(s)) delete i[s];
    else throw Error(`Key ${s} not found in enum`);
    return new pl({ ...t, checks: [], ...O.normalizeParams(n), entries: i });
  };
});
function ft(e, t) {
  let r = Array.isArray(e) ? Object.fromEntries(e.map((o) => [o, o])) : e;
  return new pl({ type: "enum", entries: r, ...O.normalizeParams(t) });
}
function sK(e, t) {
  return new pl({ type: "enum", entries: e, ...O.normalizeParams(t) });
}
var y$ = b("ZodLiteral", (e, t) => {
  Fp.init(e, t), ne.init(e, t), e.values = new Set(t.values), Object.defineProperty(e, "value", { get() {
    if (t.values.length > 1) throw Error("This schema contains multiple valid literal values. Use `.values` instead.");
    return t.values[0];
  } });
});
function B(e, t) {
  return new y$({ type: "literal", values: Array.isArray(e) ? e : [e], ...O.normalizeParams(t) });
}
var b$ = b("ZodFile", (e, t) => {
  Bp.init(e, t), ne.init(e, t), e.min = (r, o) => e.check(Co(r, o)), e.max = (r, o) => e.check(rs(r, o)), e.mime = (r, o) => e.check(sl(Array.isArray(r) ? r : [r], o));
});
function aK(e) {
  return Ef(b$, e);
}
var zv = b("ZodTransform", (e, t) => {
  Yi.init(e, t), ne.init(e, t), e._zod.parse = (r, o) => {
    r.addIssue = (i) => {
      if (typeof i === "string") r.issues.push(O.issue(i, r.value, t));
      else {
        let s = i;
        if (s.fatal) s.continue = false;
        s.code ?? (s.code = "custom"), s.input ?? (s.input = r.value), s.inst ?? (s.inst = e), s.continue ?? (s.continue = true), r.issues.push(O.issue(s));
      }
    };
    let n = t.transform(r.value, r);
    if (n instanceof Promise) return n.then((i) => (r.value = i, r));
    return r.value = n, r;
  };
});
function Lv(e) {
  return new zv({ type: "transform", transform: e });
}
var Fv = b("ZodOptional", (e, t) => {
  Hp.init(e, t), ne.init(e, t), e.unwrap = () => e._zod.def.innerType;
});
function Re(e) {
  return new Fv({ type: "optional", innerType: e });
}
var _$ = b("ZodNullable", (e, t) => {
  qp.init(e, t), ne.init(e, t), e.unwrap = () => e._zod.def.innerType;
});
function Lf(e) {
  return new _$({ type: "nullable", innerType: e });
}
function cK(e) {
  return Re(Lf(e));
}
var v$ = b("ZodDefault", (e, t) => {
  Zp.init(e, t), ne.init(e, t), e.unwrap = () => e._zod.def.innerType, e.removeDefault = e.unwrap;
});
function S$(e, t) {
  return new v$({ type: "default", innerType: e, get defaultValue() {
    return typeof t === "function" ? t() : t;
  } });
}
var x$ = b("ZodPrefault", (e, t) => {
  Vp.init(e, t), ne.init(e, t), e.unwrap = () => e._zod.def.innerType;
});
function w$(e, t) {
  return new x$({ type: "prefault", innerType: e, get defaultValue() {
    return typeof t === "function" ? t() : t;
  } });
}
var Bv = b("ZodNonOptional", (e, t) => {
  Wp.init(e, t), ne.init(e, t), e.unwrap = () => e._zod.def.innerType;
});
function k$(e, t) {
  return new Bv({ type: "nonoptional", innerType: e, ...O.normalizeParams(t) });
}
var E$ = b("ZodSuccess", (e, t) => {
  Kp.init(e, t), ne.init(e, t), e.unwrap = () => e._zod.def.innerType;
});
function lK(e) {
  return new E$({ type: "success", innerType: e });
}
var T$ = b("ZodCatch", (e, t) => {
  Gp.init(e, t), ne.init(e, t), e.unwrap = () => e._zod.def.innerType, e.removeCatch = e.unwrap;
});
function P$(e, t) {
  return new T$({ type: "catch", innerType: e, catchValue: typeof t === "function" ? t : () => t });
}
var I$ = b("ZodNaN", (e, t) => {
  Jp.init(e, t), ne.init(e, t);
});
function uK(e) {
  return kf(I$, e);
}
var Hv = b("ZodPipe", (e, t) => {
  Qi.init(e, t), ne.init(e, t), e.in = t.in, e.out = t.out;
});
function Ff(e, t) {
  return new Hv({ type: "pipe", in: e, out: t });
}
var R$ = b("ZodReadonly", (e, t) => {
  Xp.init(e, t), ne.init(e, t);
});
function $$(e) {
  return new R$({ type: "readonly", innerType: e });
}
var O$ = b("ZodTemplateLiteral", (e, t) => {
  Yp.init(e, t), ne.init(e, t);
});
function dK(e, t) {
  return new O$({ type: "template_literal", parts: e, ...O.normalizeParams(t) });
}
var A$ = b("ZodLazy", (e, t) => {
  ef.init(e, t), ne.init(e, t), e.unwrap = () => e._zod.def.getter();
});
function C$(e) {
  return new A$({ type: "lazy", getter: e });
}
var M$ = b("ZodPromise", (e, t) => {
  Qp.init(e, t), ne.init(e, t), e.unwrap = () => e._zod.def.innerType;
});
function pK(e) {
  return new M$({ type: "promise", innerType: e });
}
var Wf = b("ZodCustom", (e, t) => {
  tf.init(e, t), ne.init(e, t);
});
function D$(e, t) {
  let r = new Me({ check: "custom", ...O.normalizeParams(t) });
  return r._zod.check = e, r;
}
function qv(e, t) {
  return Tf(Wf, e ?? (() => true), t);
}
function N$(e, t = {}) {
  return Pf(Wf, e, t);
}
function j$(e, t) {
  let r = D$((o) => (o.addIssue = (n) => {
    if (typeof n === "string") o.issues.push(O.issue(n, o.value, r._zod.def));
    else {
      let i = n;
      if (i.fatal) i.continue = false;
      i.code ?? (i.code = "custom"), i.input ?? (i.input = o.value), i.inst ?? (i.inst = r), i.continue ?? (i.continue = !r._zod.def.abort), o.issues.push(O.issue(i));
    }
  }, e(o.value, o)), t);
  return r;
}
function fK(e, t = { error: `Input not instance of ${e.name}` }) {
  let r = new Wf({ type: "custom", check: "custom", fn: (o) => o instanceof e, abort: true, ...O.normalizeParams(t) });
  return r._zod.bag.Class = e, r;
}
var mK = (...e) => If({ Pipe: Hv, Boolean: gl, String: fl, Transform: zv }, ...e);
function gK(e) {
  let t = C$(() => we([S(e), fe(), Ve(), Bf(), ie(t), ke(S(), t)]));
  return t;
}
function Kf(e, t) {
  return Ff(Lv(e), t);
}
var hK = { invalid_type: "invalid_type", too_big: "too_big", too_small: "too_small", invalid_format: "invalid_format", not_multiple_of: "not_multiple_of", unrecognized_keys: "unrecognized_keys", invalid_union: "invalid_union", invalid_key: "invalid_key", invalid_element: "invalid_element", invalid_value: "invalid_value", custom: "custom" };
function yK(e) {
  He({ customError: e });
}
function bK() {
  return He().customError;
}
var Zv = {};
xr(Zv, { string: () => _K, number: () => vK, date: () => wK, boolean: () => SK, bigint: () => xK });
function _K(e) {
  return V_(fl, e);
}
function vK(e) {
  return X_(ml, e);
}
function SK(e) {
  return Y_(gl, e);
}
function xK(e) {
  return Q_(hl, e);
}
function wK(e) {
  return ev(qf, e);
}
He(Rc());
var U$ = l;
var Vv = U$;
var Nn = "io.modelcontextprotocol/related-task";
var Jf = "2.0";
var Xe = qv((e) => e !== null && (typeof e === "object" || typeof e === "function"));
var L$ = we([S(), fe().int()]);
var F$ = S();
var ZSe = ct({ ttl: fe().optional(), pollInterval: fe().optional() });
var EK = L({ ttl: fe().optional() });
var TK = L({ taskId: S() });
var Kv = ct({ progressToken: L$.optional(), [Nn]: TK.optional() });
var Ut = L({ _meta: Kv.optional() });
var bl = Ut.extend({ task: EK.optional() });
var tt = L({ method: S(), params: Ut.loose().optional() });
var Yt = L({ _meta: Kv.optional() });
var Qt = L({ method: S(), params: Yt.loose().optional() });
var rt = ct({ _meta: Kv.optional() });
var Xf = we([S(), fe().int()]);
var H$ = L({ jsonrpc: B(Jf), id: Xf, ...tt.shape }).strict();
var q$ = L({ jsonrpc: B(Jf), ...Qt.shape }).strict();
var Jv = L({ jsonrpc: B(Jf), id: Xf, result: rt }).strict();
var Z;
(function(e) {
  e[e.ConnectionClosed = -32e3] = "ConnectionClosed", e[e.RequestTimeout = -32001] = "RequestTimeout", e[e.ParseError = -32700] = "ParseError", e[e.InvalidRequest = -32600] = "InvalidRequest", e[e.MethodNotFound = -32601] = "MethodNotFound", e[e.InvalidParams = -32602] = "InvalidParams", e[e.InternalError = -32603] = "InternalError", e[e.UrlElicitationRequired = -32042] = "UrlElicitationRequired";
})(Z || (Z = {}));
var Xv = L({ jsonrpc: B(Jf), id: Xf.optional(), error: L({ code: fe().int(), message: S(), data: Oe().optional() }) }).strict();
var VSe = we([H$, q$, Jv, Xv]);
var WSe = we([Jv, Xv]);
var Yf = rt.strict();
var PK = Yt.extend({ requestId: Xf.optional(), reason: S().optional() });
var Qf = Qt.extend({ method: B("notifications/cancelled"), params: PK });
var IK = L({ src: S(), mimeType: S().optional(), sizes: ie(S()).optional(), theme: ft(["light", "dark"]).optional() });
var vl = L({ icons: ie(IK).optional() });
var us = L({ name: S(), title: S().optional() });
var W$ = us.extend({ ...us.shape, ...vl.shape, version: S(), websiteUrl: S().optional(), description: S().optional() });
var RK = yl(L({ applyDefaults: Ve().optional() }), ke(S(), Oe()));
var $K = Kf((e) => {
  if (e && typeof e === "object" && !Array.isArray(e)) {
    if (Object.keys(e).length === 0) return { form: {} };
  }
  return e;
}, yl(L({ form: RK.optional(), url: Xe.optional() }), ke(S(), Oe()).optional()));
var OK = ct({ list: Xe.optional(), cancel: Xe.optional(), requests: ct({ sampling: ct({ createMessage: Xe.optional() }).optional(), elicitation: ct({ create: Xe.optional() }).optional() }).optional() });
var AK = ct({ list: Xe.optional(), cancel: Xe.optional(), requests: ct({ tools: ct({ call: Xe.optional() }).optional() }).optional() });
var CK = L({ experimental: ke(S(), Xe).optional(), sampling: L({ context: Xe.optional(), tools: Xe.optional() }).optional(), elicitation: $K.optional(), roots: L({ listChanged: Ve().optional() }).optional(), tasks: OK.optional(), extensions: ke(S(), Xe).optional() });
var MK = Ut.extend({ protocolVersion: S(), capabilities: CK, clientInfo: W$ });
var Yv = tt.extend({ method: B("initialize"), params: MK });
var DK = L({ experimental: ke(S(), Xe).optional(), logging: Xe.optional(), completions: Xe.optional(), prompts: L({ listChanged: Ve().optional() }).optional(), resources: L({ subscribe: Ve().optional(), listChanged: Ve().optional() }).optional(), tools: L({ listChanged: Ve().optional() }).optional(), tasks: AK.optional(), extensions: ke(S(), Xe).optional() });
var NK = rt.extend({ protocolVersion: S(), capabilities: DK, serverInfo: W$, instructions: S().optional() });
var Qv = Qt.extend({ method: B("notifications/initialized"), params: Yt.optional() });
var em = tt.extend({ method: B("ping"), params: Ut.optional() });
var jK = L({ progress: fe(), total: Re(fe()), message: Re(S()) });
var UK = L({ ...Yt.shape, ...jK.shape, progressToken: L$ });
var tm = Qt.extend({ method: B("notifications/progress"), params: UK });
var zK = Ut.extend({ cursor: F$.optional() });
var Sl = tt.extend({ params: zK.optional() });
var xl = rt.extend({ nextCursor: F$.optional() });
var LK = ft(["working", "input_required", "completed", "failed", "cancelled"]);
var wl = L({ taskId: S(), status: LK, ttl: we([fe(), Bf()]), createdAt: S(), lastUpdatedAt: S(), pollInterval: Re(fe()), statusMessage: Re(S()) });
var ds = rt.extend({ task: wl });
var FK = Yt.merge(wl);
var kl = Qt.extend({ method: B("notifications/tasks/status"), params: FK });
var rm = tt.extend({ method: B("tasks/get"), params: Ut.extend({ taskId: S() }) });
var nm = rt.merge(wl);
var om = tt.extend({ method: B("tasks/result"), params: Ut.extend({ taskId: S() }) });
var KSe = rt.loose();
var im = Sl.extend({ method: B("tasks/list") });
var sm = xl.extend({ tasks: ie(wl) });
var am = tt.extend({ method: B("tasks/cancel"), params: Ut.extend({ taskId: S() }) });
var K$ = rt.merge(wl);
var G$ = L({ uri: S(), mimeType: Re(S()), _meta: ke(S(), Oe()).optional() });
var J$ = G$.extend({ text: S() });
var eS = S().refine((e) => {
  try {
    return atob(e), true;
  } catch {
    return false;
  }
}, { message: "Invalid Base64 string" });
var X$ = G$.extend({ blob: eS });
var El = ft(["user", "assistant"]);
var ps = L({ audience: ie(El).optional(), priority: fe().min(0).max(1).optional(), lastModified: as.datetime({ offset: true }).optional() });
var Y$ = L({ ...us.shape, ...vl.shape, uri: S(), description: Re(S()), mimeType: Re(S()), size: Re(fe()), annotations: ps.optional(), _meta: Re(ct({})) });
var BK = L({ ...us.shape, ...vl.shape, uriTemplate: S(), description: Re(S()), mimeType: Re(S()), annotations: ps.optional(), _meta: Re(ct({})) });
var cm = Sl.extend({ method: B("resources/list") });
var HK = xl.extend({ resources: ie(Y$) });
var lm = Sl.extend({ method: B("resources/templates/list") });
var qK = xl.extend({ resourceTemplates: ie(BK) });
var tS = Ut.extend({ uri: S() });
var ZK = tS;
var um = tt.extend({ method: B("resources/read"), params: ZK });
var VK = rt.extend({ contents: ie(we([J$, X$])) });
var WK = Qt.extend({ method: B("notifications/resources/list_changed"), params: Yt.optional() });
var KK = tS;
var GK = tt.extend({ method: B("resources/subscribe"), params: KK });
var JK = tS;
var XK = tt.extend({ method: B("resources/unsubscribe"), params: JK });
var YK = Yt.extend({ uri: S() });
var QK = Qt.extend({ method: B("notifications/resources/updated"), params: YK });
var eG = L({ name: S(), description: Re(S()), required: Re(Ve()) });
var tG = L({ ...us.shape, ...vl.shape, description: Re(S()), arguments: Re(ie(eG)), _meta: Re(ct({})) });
var dm = Sl.extend({ method: B("prompts/list") });
var rG = xl.extend({ prompts: ie(tG) });
var nG = Ut.extend({ name: S(), arguments: ke(S(), S()).optional() });
var pm = tt.extend({ method: B("prompts/get"), params: nG });
var rS = L({ type: B("text"), text: S(), annotations: ps.optional(), _meta: ke(S(), Oe()).optional() });
var nS = L({ type: B("image"), data: eS, mimeType: S(), annotations: ps.optional(), _meta: ke(S(), Oe()).optional() });
var oS = L({ type: B("audio"), data: eS, mimeType: S(), annotations: ps.optional(), _meta: ke(S(), Oe()).optional() });
var oG = L({ type: B("tool_use"), name: S(), id: S(), input: ke(S(), Oe()), _meta: ke(S(), Oe()).optional() });
var iG = L({ type: B("resource"), resource: we([J$, X$]), annotations: ps.optional(), _meta: ke(S(), Oe()).optional() });
var sG = Y$.extend({ type: B("resource_link") });
var iS = we([rS, nS, oS, sG, iG]);
var aG = L({ role: El, content: iS });
var cG = rt.extend({ description: S().optional(), messages: ie(aG) });
var lG = Qt.extend({ method: B("notifications/prompts/list_changed"), params: Yt.optional() });
var uG = L({ title: S().optional(), readOnlyHint: Ve().optional(), destructiveHint: Ve().optional(), idempotentHint: Ve().optional(), openWorldHint: Ve().optional() });
var dG = L({ taskSupport: ft(["required", "optional", "forbidden"]).optional() });
var Q$ = L({ ...us.shape, ...vl.shape, description: S().optional(), inputSchema: L({ type: B("object"), properties: ke(S(), Xe).optional(), required: ie(S()).optional() }).catchall(Oe()), outputSchema: L({ type: B("object"), properties: ke(S(), Xe).optional(), required: ie(S()).optional() }).catchall(Oe()).optional(), annotations: uG.optional(), execution: dG.optional(), _meta: ke(S(), Oe()).optional() });
var fm = Sl.extend({ method: B("tools/list") });
var pG = xl.extend({ tools: ie(Q$) });
var mm = rt.extend({ content: ie(iS).default([]), structuredContent: ke(S(), Oe()).optional(), isError: Ve().optional() });
var GSe = mm.or(rt.extend({ toolResult: Oe() }));
var fG = bl.extend({ name: S(), arguments: ke(S(), Oe()).optional() });
var fs = tt.extend({ method: B("tools/call"), params: fG });
var mG = Qt.extend({ method: B("notifications/tools/list_changed"), params: Yt.optional() });
var JSe = L({ autoRefresh: Ve().default(true), debounceMs: fe().int().nonnegative().default(300) });
var Tl = ft(["debug", "info", "notice", "warning", "error", "critical", "alert", "emergency"]);
var gG = Ut.extend({ level: Tl });
var sS = tt.extend({ method: B("logging/setLevel"), params: gG });
var hG = Yt.extend({ level: Tl, logger: S().optional(), data: Oe() });
var yG = Qt.extend({ method: B("notifications/message"), params: hG });
var bG = L({ name: S().optional() });
var _G = L({ hints: ie(bG).optional(), costPriority: fe().min(0).max(1).optional(), speedPriority: fe().min(0).max(1).optional(), intelligencePriority: fe().min(0).max(1).optional() });
var vG = L({ mode: ft(["auto", "required", "none"]).optional() });
var SG = L({ type: B("tool_result"), toolUseId: S().describe("The unique identifier for the corresponding tool call."), content: ie(iS).default([]), structuredContent: L({}).loose().optional(), isError: Ve().optional(), _meta: ke(S(), Oe()).optional() });
var xG = Vf("type", [rS, nS, oS]);
var Gf = Vf("type", [rS, nS, oS, oG, SG]);
var wG = L({ role: El, content: we([Gf, ie(Gf)]), _meta: ke(S(), Oe()).optional() });
var kG = bl.extend({ messages: ie(wG), modelPreferences: _G.optional(), systemPrompt: S().optional(), includeContext: ft(["none", "thisServer", "allServers"]).optional(), temperature: fe().optional(), maxTokens: fe().int(), stopSequences: ie(S()).optional(), metadata: Xe.optional(), tools: ie(Q$).optional(), toolChoice: vG.optional() });
var EG = tt.extend({ method: B("sampling/createMessage"), params: kG });
var Pl = rt.extend({ model: S(), stopReason: Re(ft(["endTurn", "stopSequence", "maxTokens"]).or(S())), role: El, content: xG });
var aS = rt.extend({ model: S(), stopReason: Re(ft(["endTurn", "stopSequence", "maxTokens", "toolUse"]).or(S())), role: El, content: we([Gf, ie(Gf)]) });
var TG = L({ type: B("boolean"), title: S().optional(), description: S().optional(), default: Ve().optional() });
var PG = L({ type: B("string"), title: S().optional(), description: S().optional(), minLength: fe().optional(), maxLength: fe().optional(), format: ft(["email", "uri", "date", "date-time"]).optional(), default: S().optional() });
var IG = L({ type: ft(["number", "integer"]), title: S().optional(), description: S().optional(), minimum: fe().optional(), maximum: fe().optional(), default: fe().optional() });
var RG = L({ type: B("string"), title: S().optional(), description: S().optional(), enum: ie(S()), default: S().optional() });
var $G = L({ type: B("string"), title: S().optional(), description: S().optional(), oneOf: ie(L({ const: S(), title: S() })), default: S().optional() });
var OG = L({ type: B("string"), title: S().optional(), description: S().optional(), enum: ie(S()), enumNames: ie(S()).optional(), default: S().optional() });
var AG = we([RG, $G]);
var CG = L({ type: B("array"), title: S().optional(), description: S().optional(), minItems: fe().optional(), maxItems: fe().optional(), items: L({ type: B("string"), enum: ie(S()) }), default: ie(S()).optional() });
var MG = L({ type: B("array"), title: S().optional(), description: S().optional(), minItems: fe().optional(), maxItems: fe().optional(), items: L({ anyOf: ie(L({ const: S(), title: S() })) }), default: ie(S()).optional() });
var DG = we([CG, MG]);
var NG = we([OG, AG, DG]);
var jG = we([NG, TG, PG, IG]);
var UG = bl.extend({ mode: B("form").optional(), message: S(), requestedSchema: L({ type: B("object"), properties: ke(S(), jG), required: ie(S()).optional() }) });
var zG = bl.extend({ mode: B("url"), message: S(), elicitationId: S(), url: S().url() });
var LG = we([UG, zG]);
var FG = tt.extend({ method: B("elicitation/create"), params: LG });
var BG = Yt.extend({ elicitationId: S() });
var HG = Qt.extend({ method: B("notifications/elicitation/complete"), params: BG });
var ms = rt.extend({ action: ft(["accept", "decline", "cancel"]), content: Kf((e) => e === null ? void 0 : e, ke(S(), we([S(), fe(), Ve(), ie(S())])).optional()) });
var qG = L({ type: B("ref/resource"), uri: S() });
var ZG = L({ type: B("ref/prompt"), name: S() });
var VG = Ut.extend({ ref: we([ZG, qG]), argument: L({ name: S(), value: S() }), context: L({ arguments: ke(S(), S()).optional() }).optional() });
var gm = tt.extend({ method: B("completion/complete"), params: VG });
var WG = rt.extend({ completion: ct({ values: ie(S()).max(100), total: Re(fe().int()), hasMore: Re(Ve()) }) });
var KG = L({ uri: S().startsWith("file://"), name: S().optional(), _meta: ke(S(), Oe()).optional() });
var GG = tt.extend({ method: B("roots/list"), params: Ut.optional() });
var cS = rt.extend({ roots: ie(KG) });
var JG = Qt.extend({ method: B("notifications/roots/list_changed"), params: Yt.optional() });
var XSe = we([em, Yv, gm, sS, pm, dm, cm, lm, um, GK, XK, fs, fm, rm, om, im, am]);
var YSe = we([Qf, tm, Qv, JG, kl]);
var QSe = we([Yf, Pl, aS, ms, cS, nm, sm, ds]);
var exe = we([em, EG, FG, GG, rm, om, im, am]);
var txe = we([Qf, tm, yG, QK, WK, mG, lG, kl, HG]);
var rxe = we([Yf, NK, WG, cG, rG, HK, qK, VK, mm, pG, nm, sm, ds]);
var oO = Symbol("Let zodToJsonSchema decide on which parser to use");
var QG = new Set("ABCDEFGHIJKLMNOPQRSTUVXYZabcdefghijklmnopqrstuvxyz0123456789");
var bN = Tg(px(), 1);
var _N = Tg(yN(), 1);
var wN = Symbol.for("mcp.completable");
var xN;
(function(e) {
  e.Completable = "McpCompletable";
})(xN || (xN = {}));
function P(e) {
  let t;
  return () => t ??= e();
}
var jQ = P(() => l.object({ session_id: l.string(), ws_url: l.string(), work_dir: l.string().optional(), session_key: l.string().optional() }));
var AN;
(function(e) {
  e[e.lineFeed = 10] = "lineFeed", e[e.carriageReturn = 13] = "carriageReturn", e[e.space = 32] = "space", e[e._0 = 48] = "_0", e[e._1 = 49] = "_1", e[e._2 = 50] = "_2", e[e._3 = 51] = "_3", e[e._4 = 52] = "_4", e[e._5 = 53] = "_5", e[e._6 = 54] = "_6", e[e._7 = 55] = "_7", e[e._8 = 56] = "_8", e[e._9 = 57] = "_9", e[e.a = 97] = "a", e[e.b = 98] = "b", e[e.c = 99] = "c", e[e.d = 100] = "d", e[e.e = 101] = "e", e[e.f = 102] = "f", e[e.g = 103] = "g", e[e.h = 104] = "h", e[e.i = 105] = "i", e[e.j = 106] = "j", e[e.k = 107] = "k", e[e.l = 108] = "l", e[e.m = 109] = "m", e[e.n = 110] = "n", e[e.o = 111] = "o", e[e.p = 112] = "p", e[e.q = 113] = "q", e[e.r = 114] = "r", e[e.s = 115] = "s", e[e.t = 116] = "t", e[e.u = 117] = "u", e[e.v = 118] = "v", e[e.w = 119] = "w", e[e.x = 120] = "x", e[e.y = 121] = "y", e[e.z = 122] = "z", e[e.A = 65] = "A", e[e.B = 66] = "B", e[e.C = 67] = "C", e[e.D = 68] = "D", e[e.E = 69] = "E", e[e.F = 70] = "F", e[e.G = 71] = "G", e[e.H = 72] = "H", e[e.I = 73] = "I", e[e.J = 74] = "J", e[e.K = 75] = "K", e[e.L = 76] = "L", e[e.M = 77] = "M", e[e.N = 78] = "N", e[e.O = 79] = "O", e[e.P = 80] = "P", e[e.Q = 81] = "Q", e[e.R = 82] = "R", e[e.S = 83] = "S", e[e.T = 84] = "T", e[e.U = 85] = "U", e[e.V = 86] = "V", e[e.W = 87] = "W", e[e.X = 88] = "X", e[e.Y = 89] = "Y", e[e.Z = 90] = "Z", e[e.asterisk = 42] = "asterisk", e[e.backslash = 92] = "backslash", e[e.closeBrace = 125] = "closeBrace", e[e.closeBracket = 93] = "closeBracket", e[e.colon = 58] = "colon", e[e.comma = 44] = "comma", e[e.dot = 46] = "dot", e[e.doubleQuote = 34] = "doubleQuote", e[e.minus = 45] = "minus", e[e.openBrace = 123] = "openBrace", e[e.openBracket = 91] = "openBracket", e[e.plus = 43] = "plus", e[e.slash = 47] = "slash", e[e.formFeed = 12] = "formFeed", e[e.tab = 9] = "tab";
})(AN || (AN = {}));
var VQ = Array(20).fill(0).map((e, t) => " ".repeat(t));
var WQ = { " ": { "\n": Array(200).fill(0).map((e, t) => `
` + " ".repeat(t)), "\r": Array(200).fill(0).map((e, t) => "\r" + " ".repeat(t)), "\r\n": Array(200).fill(0).map((e, t) => `\r
` + " ".repeat(t)) }, "	": { "\n": Array(200).fill(0).map((e, t) => `
` + "	".repeat(t)), "\r": Array(200).fill(0).map((e, t) => "\r" + "	".repeat(t)), "\r\n": Array(200).fill(0).map((e, t) => `\r
` + "	".repeat(t)) } };
var MN;
(function(e) {
  e.DEFAULT = { allowTrailingComma: false };
})(MN || (MN = {}));
var DN;
(function(e) {
  e[e.None = 0] = "None", e[e.UnexpectedEndOfComment = 1] = "UnexpectedEndOfComment", e[e.UnexpectedEndOfString = 2] = "UnexpectedEndOfString", e[e.UnexpectedEndOfNumber = 3] = "UnexpectedEndOfNumber", e[e.InvalidUnicode = 4] = "InvalidUnicode", e[e.InvalidEscapeCharacter = 5] = "InvalidEscapeCharacter", e[e.InvalidCharacter = 6] = "InvalidCharacter";
})(DN || (DN = {}));
var NN;
(function(e) {
  e[e.OpenBraceToken = 1] = "OpenBraceToken", e[e.CloseBraceToken = 2] = "CloseBraceToken", e[e.OpenBracketToken = 3] = "OpenBracketToken", e[e.CloseBracketToken = 4] = "CloseBracketToken", e[e.CommaToken = 5] = "CommaToken", e[e.ColonToken = 6] = "ColonToken", e[e.NullKeyword = 7] = "NullKeyword", e[e.TrueKeyword = 8] = "TrueKeyword", e[e.FalseKeyword = 9] = "FalseKeyword", e[e.StringLiteral = 10] = "StringLiteral", e[e.NumericLiteral = 11] = "NumericLiteral", e[e.LineCommentTrivia = 12] = "LineCommentTrivia", e[e.BlockCommentTrivia = 13] = "BlockCommentTrivia", e[e.LineBreakTrivia = 14] = "LineBreakTrivia", e[e.Trivia = 15] = "Trivia", e[e.Unknown = 16] = "Unknown", e[e.EOF = 17] = "EOF";
})(NN || (NN = {}));
var jN;
(function(e) {
  e[e.InvalidSymbol = 1] = "InvalidSymbol", e[e.InvalidNumberFormat = 2] = "InvalidNumberFormat", e[e.PropertyNameExpected = 3] = "PropertyNameExpected", e[e.ValueExpected = 4] = "ValueExpected", e[e.ColonExpected = 5] = "ColonExpected", e[e.CommaExpected = 6] = "CommaExpected", e[e.CloseBraceExpected = 7] = "CloseBraceExpected", e[e.CloseBracketExpected = 8] = "CloseBracketExpected", e[e.EndOfFileExpected = 9] = "EndOfFileExpected", e[e.InvalidCommentToken = 10] = "InvalidCommentToken", e[e.UnexpectedEndOfComment = 11] = "UnexpectedEndOfComment", e[e.UnexpectedEndOfString = 12] = "UnexpectedEndOfString", e[e.UnexpectedEndOfNumber = 13] = "UnexpectedEndOfNumber", e[e.InvalidUnicode = 14] = "InvalidUnicode", e[e.InvalidEscapeCharacter = 15] = "InvalidEscapeCharacter", e[e.InvalidCharacter = 16] = "InvalidCharacter";
})(jN || (jN = {}));
function rg(e) {
  return e.startsWith("\uFEFF") ? e.slice(1) : e;
}
var Vn = UN.homedir();
var Ox = UN.tmpdir();
var { env: Is } = $x;
var eee = (e) => {
  let t = Ne.join(Vn, "Library");
  return { data: Ne.join(t, "Application Support", e), config: Ne.join(t, "Preferences", e), cache: Ne.join(t, "Caches", e), log: Ne.join(t, "Logs", e), temp: Ne.join(Ox, e) };
};
var tee = (e) => {
  let t = Is.APPDATA || Ne.join(Vn, "AppData", "Roaming"), r = Is.LOCALAPPDATA || Ne.join(Vn, "AppData", "Local");
  return { data: Ne.join(r, e, "Data"), config: Ne.join(t, e, "Config"), cache: Ne.join(r, e, "Cache"), log: Ne.join(r, e, "Log"), temp: Ne.join(Ox, e) };
};
var ree = (e) => {
  let t = Ne.basename(Vn);
  return { data: Ne.join(Is.XDG_DATA_HOME || Ne.join(Vn, ".local", "share"), e), config: Ne.join(Is.XDG_CONFIG_HOME || Ne.join(Vn, ".config"), e), cache: Ne.join(Is.XDG_CACHE_HOME || Ne.join(Vn, ".cache"), e), log: Ne.join(Is.XDG_STATE_HOME || Ne.join(Vn, ".local", "state"), e), temp: Ne.join(Ox, t, e) };
};
function Ax(e, { suffix: t = "nodejs" } = {}) {
  if (typeof e !== "string") throw TypeError(`Expected a string, got ${typeof e}`);
  if (t) e += `-${t}`;
  if ($x.platform === "darwin") return eee(e);
  if ($x.platform === "win32") return tee(e);
  return ree(e);
}
var dIe = Ax("claude-cli");
function nee() {
  if (process.env.CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC) return "essential-traffic";
  if (process.env.DISABLE_TELEMETRY) return "no-telemetry";
  if (Ee(process.env.DO_NOT_TRACK)) return "no-telemetry";
  return "default";
}
function zN() {
  return nee() === "essential-traffic";
}
var oee = 100;
var Cx = [];
function iee(e) {
  if (Cx.length >= oee) Cx.shift();
  Cx.push(e);
}
var see = [];
var LN = null;
var NIe = Ce(() => process.argv.includes("--hard-fail"));
function ng(e) {
  let t = kr(e);
  try {
    if (Ee(process.env.CLAUDE_CODE_USE_BEDROCK) || Ee(process.env.CLAUDE_CODE_USE_VERTEX) || Ee(process.env.CLAUDE_CODE_USE_FOUNDRY) || Ee(process.env.CLAUDE_CODE_USE_ANTHROPIC_AWS) || Ee(process.env.CLAUDE_CODE_USE_MANTLE) || process.env.DISABLE_ERROR_REPORTING || zN()) return;
    let o = { error: t.stack || t.message, timestamp: (/* @__PURE__ */ new Date()).toISOString() };
    if (iee(o), LN === null) {
      see.push({ type: "error", error: t });
      return;
    }
    LN.logError(t);
  } catch {
  }
}
var Rs = typeof performance === "object" && performance && typeof performance.now === "function" ? performance : Date;
var BN = /* @__PURE__ */ new Set();
var Mx = typeof process === "object" && !!process ? process : {};
var HN = (e, t, r, o) => {
  typeof Mx.emitWarning === "function" ? Mx.emitWarning(e, t, r, o) : console.error(`[${r}] ${t}: ${e}`);
};
var og = globalThis.AbortController;
var FN = globalThis.AbortSignal;
if (typeof og > "u") {
  FN = class {
    onabort;
    _onabort = [];
    reason;
    aborted = false;
    addEventListener(o, n) {
      this._onabort.push(n);
    }
  }, og = class {
    constructor() {
      t();
    }
    signal = new FN();
    abort(o) {
      if (this.signal.aborted) return;
      this.signal.reason = o, this.signal.aborted = true;
      for (let n of this.signal._onabort) n(o);
      this.signal.onabort?.(o);
    }
  };
  let e = Mx.env?.LRU_CACHE_IGNORE_AC_WARNING !== "1", t = () => {
    if (!e) return;
    e = false, HN("AbortController is not defined. If using lru-cache in node 14, load an AbortController polyfill from the `node-abort-controller` package. A minimal polyfill is provided for use by LRUCache.fetch(), but it should not be relied upon in other contexts (eg, passing it to other APIs that use AbortController/AbortSignal might have undesirable effects). You may disable this with LRU_CACHE_IGNORE_AC_WARNING=1 in the env.", "NO_ABORT_CONTROLLER", "ENOTSUP", t);
  };
}
var aee = (e) => !BN.has(e);
var UIe = Symbol("type");
var Wn = (e) => e && e === Math.floor(e) && e > 0 && isFinite(e);
var qN = (e) => !Wn(e) ? null : e <= Math.pow(2, 8) ? Uint8Array : e <= Math.pow(2, 16) ? Uint16Array : e <= Math.pow(2, 32) ? Uint32Array : e <= Number.MAX_SAFE_INTEGER ? eu : null;
var eu = class extends Array {
  constructor(e) {
    super(e);
    this.fill(0);
  }
};
var $s = class _$s {
  heap;
  length;
  static #n = false;
  static create(e) {
    let t = qN(e);
    if (!t) return [];
    _$s.#n = true;
    let r = new _$s(e, t);
    return _$s.#n = false, r;
  }
  constructor(e, t) {
    if (!_$s.#n) throw TypeError("instantiate Stack using Stack.create(n)");
    this.heap = new t(e), this.length = 0;
  }
  push(e) {
    this.heap[this.length++] = e;
  }
  pop() {
    return this.heap[--this.length];
  }
};
var ig = class _ig {
  #n;
  #d;
  #g;
  #h;
  #$;
  #O;
  ttl;
  ttlResolution;
  ttlAutopurge;
  updateAgeOnGet;
  updateAgeOnHas;
  allowStale;
  noDisposeOnSet;
  noUpdateTTL;
  maxEntrySize;
  sizeCalculation;
  noDeleteOnFetchRejection;
  noDeleteOnStaleGet;
  allowStaleOnFetchAbort;
  allowStaleOnFetchRejection;
  ignoreFetchAbort;
  #i;
  #y;
  #o;
  #r;
  #e;
  #l;
  #p;
  #c;
  #s;
  #b;
  #a;
  #_;
  #v;
  #f;
  #S;
  #T;
  #u;
  static unsafeExposeInternals(e) {
    return { starts: e.#v, ttls: e.#f, sizes: e.#_, keyMap: e.#o, keyList: e.#r, valList: e.#e, next: e.#l, prev: e.#p, get head() {
      return e.#c;
    }, get tail() {
      return e.#s;
    }, free: e.#b, isBackgroundFetch: (t) => e.#t(t), backgroundFetch: (t, r, o, n) => e.#M(t, r, o, n), moveToTail: (t) => e.#R(t), indexes: (t) => e.#x(t), rindexes: (t) => e.#w(t), isStale: (t) => e.#m(t) };
  }
  get max() {
    return this.#n;
  }
  get maxSize() {
    return this.#d;
  }
  get calculatedSize() {
    return this.#y;
  }
  get size() {
    return this.#i;
  }
  get fetchMethod() {
    return this.#$;
  }
  get memoMethod() {
    return this.#O;
  }
  get dispose() {
    return this.#g;
  }
  get disposeAfter() {
    return this.#h;
  }
  constructor(e) {
    let { max: t = 0, ttl: r, ttlResolution: o = 1, ttlAutopurge: n, updateAgeOnGet: i, updateAgeOnHas: s, allowStale: a, dispose: c, disposeAfter: u, noDisposeOnSet: d, noUpdateTTL: p, maxSize: f = 0, maxEntrySize: m = 0, sizeCalculation: g, fetchMethod: h, memoMethod: y, noDeleteOnFetchRejection: v, noDeleteOnStaleGet: x, allowStaleOnFetchRejection: w, allowStaleOnFetchAbort: A, ignoreFetchAbort: U } = e;
    if (t !== 0 && !Wn(t)) throw TypeError("max option must be a nonnegative integer");
    let se = t ? qN(t) : Array;
    if (!se) throw Error("invalid max value: " + t);
    if (this.#n = t, this.#d = f, this.maxEntrySize = m || this.#d, this.sizeCalculation = g, this.sizeCalculation) {
      if (!this.#d && !this.maxEntrySize) throw TypeError("cannot set sizeCalculation without setting maxSize or maxEntrySize");
      if (typeof this.sizeCalculation !== "function") throw TypeError("sizeCalculation set to non-function");
    }
    if (y !== void 0 && typeof y !== "function") throw TypeError("memoMethod must be a function if defined");
    if (this.#O = y, h !== void 0 && typeof h !== "function") throw TypeError("fetchMethod must be a function if specified");
    if (this.#$ = h, this.#T = !!h, this.#o = /* @__PURE__ */ new Map(), this.#r = Array(t).fill(void 0), this.#e = Array(t).fill(void 0), this.#l = new se(t), this.#p = new se(t), this.#c = 0, this.#s = 0, this.#b = $s.create(t), this.#i = 0, this.#y = 0, typeof c === "function") this.#g = c;
    if (typeof u === "function") this.#h = u, this.#a = [];
    else this.#h = void 0, this.#a = void 0;
    if (this.#S = !!this.#g, this.#u = !!this.#h, this.noDisposeOnSet = !!d, this.noUpdateTTL = !!p, this.noDeleteOnFetchRejection = !!v, this.allowStaleOnFetchRejection = !!w, this.allowStaleOnFetchAbort = !!A, this.ignoreFetchAbort = !!U, this.maxEntrySize !== 0) {
      if (this.#d !== 0) {
        if (!Wn(this.#d)) throw TypeError("maxSize must be a positive integer if specified");
      }
      if (!Wn(this.maxEntrySize)) throw TypeError("maxEntrySize must be a positive integer if specified");
      this.#F();
    }
    if (this.allowStale = !!a, this.noDeleteOnStaleGet = !!x, this.updateAgeOnGet = !!i, this.updateAgeOnHas = !!s, this.ttlResolution = Wn(o) || o === 0 ? o : 1, this.ttlAutopurge = !!n, this.ttl = r || 0, this.ttl) {
      if (!Wn(this.ttl)) throw TypeError("ttl must be a positive integer if specified");
      this.#D();
    }
    if (this.#n === 0 && this.ttl === 0 && this.#d === 0) throw TypeError("At least one of max, maxSize, or ttl is required");
    if (!this.ttlAutopurge && !this.#n && !this.#d) {
      if (aee("LRU_CACHE_UNBOUNDED")) BN.add("LRU_CACHE_UNBOUNDED"), HN("TTL caching without ttlAutopurge, max, or maxSize can result in unbounded memory consumption.", "UnboundedCacheWarning", "LRU_CACHE_UNBOUNDED", _ig);
    }
  }
  getRemainingTTL(e) {
    return this.#o.has(e) ? 1 / 0 : 0;
  }
  #D() {
    let e = new eu(this.#n), t = new eu(this.#n);
    this.#f = e, this.#v = t, this.#N = (n, i, s = Rs.now()) => {
      if (t[n] = i !== 0 ? s : 0, e[n] = i, i !== 0 && this.ttlAutopurge) {
        let a = setTimeout(() => {
          if (this.#m(n)) this.#k(this.#r[n], "expire");
        }, i + 1);
        if (a.unref) a.unref();
      }
    }, this.#P = (n) => {
      t[n] = e[n] !== 0 ? Rs.now() : 0;
    }, this.#E = (n, i) => {
      if (e[i]) {
        let s = e[i], a = t[i];
        if (!s || !a) return;
        n.ttl = s, n.start = a, n.now = r || o();
        let c = n.now - a;
        n.remainingTTL = s - c;
      }
    };
    let r = 0, o = () => {
      let n = Rs.now();
      if (this.ttlResolution > 0) {
        r = n;
        let i = setTimeout(() => r = 0, this.ttlResolution);
        if (i.unref) i.unref();
      }
      return n;
    };
    this.getRemainingTTL = (n) => {
      let i = this.#o.get(n);
      if (i === void 0) return 0;
      let s = e[i], a = t[i];
      if (!s || !a) return 1 / 0;
      let c = (r || o()) - a;
      return s - c;
    }, this.#m = (n) => {
      let i = t[n], s = e[n];
      return !!s && !!i && (r || o()) - i > s;
    };
  }
  #P = () => {
  };
  #E = () => {
  };
  #N = () => {
  };
  #m = () => false;
  #F() {
    let e = new eu(this.#n);
    this.#y = 0, this.#_ = e, this.#I = (t) => {
      this.#y -= e[t], e[t] = 0;
    }, this.#j = (t, r, o, n) => {
      if (this.#t(r)) return 0;
      if (!Wn(o)) if (n) {
        if (typeof n !== "function") throw TypeError("sizeCalculation must be a function");
        if (o = n(r, t), !Wn(o)) throw TypeError("sizeCalculation return invalid (expect positive integer)");
      } else throw TypeError("invalid size value (must be positive integer). When maxSize or maxEntrySize is used, sizeCalculation or size must be set.");
      return o;
    }, this.#A = (t, r, o) => {
      if (e[t] = r, this.#d) {
        let n = this.#d - e[t];
        while (this.#y > n) this.#C(true);
      }
      if (this.#y += e[t], o) o.entrySize = r, o.totalCalculatedSize = this.#y;
    };
  }
  #I = (e) => {
  };
  #A = (e, t, r) => {
  };
  #j = (e, t, r, o) => {
    if (r || o) throw TypeError("cannot set size without setting maxSize or maxEntrySize on cache");
    return 0;
  };
  *#x({ allowStale: e = this.allowStale } = {}) {
    if (this.#i) for (let t = this.#s; ; ) {
      if (!this.#U(t)) break;
      if (e || !this.#m(t)) yield t;
      if (t === this.#c) break;
      else t = this.#p[t];
    }
  }
  *#w({ allowStale: e = this.allowStale } = {}) {
    if (this.#i) for (let t = this.#c; ; ) {
      if (!this.#U(t)) break;
      if (e || !this.#m(t)) yield t;
      if (t === this.#s) break;
      else t = this.#l[t];
    }
  }
  #U(e) {
    return e !== void 0 && this.#o.get(this.#r[e]) === e;
  }
  *entries() {
    for (let e of this.#x()) if (this.#e[e] !== void 0 && this.#r[e] !== void 0 && !this.#t(this.#e[e])) yield [this.#r[e], this.#e[e]];
  }
  *rentries() {
    for (let e of this.#w()) if (this.#e[e] !== void 0 && this.#r[e] !== void 0 && !this.#t(this.#e[e])) yield [this.#r[e], this.#e[e]];
  }
  *keys() {
    for (let e of this.#x()) {
      let t = this.#r[e];
      if (t !== void 0 && !this.#t(this.#e[e])) yield t;
    }
  }
  *rkeys() {
    for (let e of this.#w()) {
      let t = this.#r[e];
      if (t !== void 0 && !this.#t(this.#e[e])) yield t;
    }
  }
  *values() {
    for (let e of this.#x()) if (this.#e[e] !== void 0 && !this.#t(this.#e[e])) yield this.#e[e];
  }
  *rvalues() {
    for (let e of this.#w()) if (this.#e[e] !== void 0 && !this.#t(this.#e[e])) yield this.#e[e];
  }
  [Symbol.iterator]() {
    return this.entries();
  }
  [Symbol.toStringTag] = "LRUCache";
  find(e, t = {}) {
    for (let r of this.#x()) {
      let o = this.#e[r], n = this.#t(o) ? o.__staleWhileFetching : o;
      if (n === void 0) continue;
      if (e(n, this.#r[r], this)) return this.get(this.#r[r], t);
    }
  }
  forEach(e, t = this) {
    for (let r of this.#x()) {
      let o = this.#e[r], n = this.#t(o) ? o.__staleWhileFetching : o;
      if (n === void 0) continue;
      e.call(t, n, this.#r[r], this);
    }
  }
  rforEach(e, t = this) {
    for (let r of this.#w()) {
      let o = this.#e[r], n = this.#t(o) ? o.__staleWhileFetching : o;
      if (n === void 0) continue;
      e.call(t, n, this.#r[r], this);
    }
  }
  purgeStale() {
    let e = false;
    for (let t of this.#w({ allowStale: true })) if (this.#m(t)) this.#k(this.#r[t], "expire"), e = true;
    return e;
  }
  info(e) {
    let t = this.#o.get(e);
    if (t === void 0) return;
    let r = this.#e[t], o = this.#t(r) ? r.__staleWhileFetching : r;
    if (o === void 0) return;
    let n = { value: o };
    if (this.#f && this.#v) {
      let i = this.#f[t], s = this.#v[t];
      if (i && s) {
        let a = i - (Rs.now() - s);
        n.ttl = a, n.start = Date.now();
      }
    }
    if (this.#_) n.size = this.#_[t];
    return n;
  }
  dump() {
    let e = [];
    for (let t of this.#x({ allowStale: true })) {
      let r = this.#r[t], o = this.#e[t], n = this.#t(o) ? o.__staleWhileFetching : o;
      if (n === void 0 || r === void 0) continue;
      let i = { value: n };
      if (this.#f && this.#v) {
        i.ttl = this.#f[t];
        let s = Rs.now() - this.#v[t];
        i.start = Math.floor(Date.now() - s);
      }
      if (this.#_) i.size = this.#_[t];
      e.unshift([r, i]);
    }
    return e;
  }
  load(e) {
    this.clear();
    for (let [t, r] of e) {
      if (r.start) {
        let o = Date.now() - r.start;
        r.start = Rs.now() - o;
      }
      this.set(t, r.value, r);
    }
  }
  set(e, t, r = {}) {
    if (t === void 0) return this.delete(e), this;
    let { ttl: o = this.ttl, start: n, noDisposeOnSet: i = this.noDisposeOnSet, sizeCalculation: s = this.sizeCalculation, status: a } = r, { noUpdateTTL: c = this.noUpdateTTL } = r, u = this.#j(e, t, r.size || 0, s);
    if (this.maxEntrySize && u > this.maxEntrySize) {
      if (a) a.set = "miss", a.maxEntrySizeExceeded = true;
      return this.#k(e, "set"), this;
    }
    let d = this.#i === 0 ? void 0 : this.#o.get(e);
    if (d === void 0) {
      if (d = this.#i === 0 ? this.#s : this.#b.length !== 0 ? this.#b.pop() : this.#i === this.#n ? this.#C(false) : this.#i, this.#r[d] = e, this.#e[d] = t, this.#o.set(e, d), this.#l[this.#s] = d, this.#p[d] = this.#s, this.#s = d, this.#i++, this.#A(d, u, a), a) a.set = "add";
      c = false;
    } else {
      this.#R(d);
      let p = this.#e[d];
      if (t !== p) {
        if (this.#T && this.#t(p)) {
          p.__abortController.abort(Error("replaced"));
          let { __staleWhileFetching: f } = p;
          if (f !== void 0 && !i) {
            if (this.#S) this.#g?.(f, e, "set");
            if (this.#u) this.#a?.push([f, e, "set"]);
          }
        } else if (!i) {
          if (this.#S) this.#g?.(p, e, "set");
          if (this.#u) this.#a?.push([p, e, "set"]);
        }
        if (this.#I(d), this.#A(d, u, a), this.#e[d] = t, a) {
          a.set = "replace";
          let f = p && this.#t(p) ? p.__staleWhileFetching : p;
          if (f !== void 0) a.oldValue = f;
        }
      } else if (a) a.set = "update";
    }
    if (o !== 0 && !this.#f) this.#D();
    if (this.#f) {
      if (!c) this.#N(d, o, n);
      if (a) this.#E(a, d);
    }
    if (!i && this.#u && this.#a) {
      let p = this.#a, f;
      while (f = p?.shift()) this.#h?.(...f);
    }
    return this;
  }
  pop() {
    try {
      while (this.#i) {
        let e = this.#e[this.#c];
        if (this.#C(true), this.#t(e)) {
          if (e.__staleWhileFetching) return e.__staleWhileFetching;
        } else if (e !== void 0) return e;
      }
    } finally {
      if (this.#u && this.#a) {
        let e = this.#a, t;
        while (t = e?.shift()) this.#h?.(...t);
      }
    }
  }
  #C(e) {
    let t = this.#c, r = this.#r[t], o = this.#e[t];
    if (this.#T && this.#t(o)) o.__abortController.abort(Error("evicted"));
    else if (this.#S || this.#u) {
      if (this.#S) this.#g?.(o, r, "evict");
      if (this.#u) this.#a?.push([o, r, "evict"]);
    }
    if (this.#I(t), e) this.#r[t] = void 0, this.#e[t] = void 0, this.#b.push(t);
    if (this.#i === 1) this.#c = this.#s = 0, this.#b.length = 0;
    else this.#c = this.#l[t];
    return this.#o.delete(r), this.#i--, t;
  }
  has(e, t = {}) {
    let { updateAgeOnHas: r = this.updateAgeOnHas, status: o } = t, n = this.#o.get(e);
    if (n !== void 0) {
      let i = this.#e[n];
      if (this.#t(i) && i.__staleWhileFetching === void 0) return false;
      if (!this.#m(n)) {
        if (r) this.#P(n);
        if (o) o.has = "hit", this.#E(o, n);
        return true;
      } else if (o) o.has = "stale", this.#E(o, n);
    } else if (o) o.has = "miss";
    return false;
  }
  peek(e, t = {}) {
    let { allowStale: r = this.allowStale } = t, o = this.#o.get(e);
    if (o === void 0 || !r && this.#m(o)) return;
    let n = this.#e[o];
    return this.#t(n) ? n.__staleWhileFetching : n;
  }
  #M(e, t, r, o) {
    let n = t === void 0 ? void 0 : this.#e[t];
    if (this.#t(n)) return n;
    let i = new og(), { signal: s } = r;
    s?.addEventListener("abort", () => i.abort(s.reason), { signal: i.signal });
    let a = { signal: i.signal, options: r, context: o }, c = (g, h = false) => {
      let { aborted: y } = i.signal, v = r.ignoreFetchAbort && g !== void 0;
      if (r.status) if (y && !h) {
        if (r.status.fetchAborted = true, r.status.fetchError = i.signal.reason, v) r.status.fetchAbortIgnored = true;
      } else r.status.fetchResolved = true;
      if (y && !v && !h) return d(i.signal.reason);
      let x = f;
      if (this.#e[t] === f) if (g === void 0) if (x.__staleWhileFetching) this.#e[t] = x.__staleWhileFetching;
      else this.#k(e, "fetch");
      else {
        if (r.status) r.status.fetchUpdated = true;
        this.set(e, g, a.options);
      }
      return g;
    }, u = (g) => {
      if (r.status) r.status.fetchRejected = true, r.status.fetchError = g;
      return d(g);
    }, d = (g) => {
      let { aborted: h } = i.signal, y = h && r.allowStaleOnFetchAbort, v = y || r.allowStaleOnFetchRejection, x = v || r.noDeleteOnFetchRejection, w = f;
      if (this.#e[t] === f) {
        if (!x || w.__staleWhileFetching === void 0) this.#k(e, "fetch");
        else if (!y) this.#e[t] = w.__staleWhileFetching;
      }
      if (v) {
        if (r.status && w.__staleWhileFetching !== void 0) r.status.returnedStale = true;
        return w.__staleWhileFetching;
      } else if (w.__returned === w) throw g;
    }, p = (g, h) => {
      let y = this.#$?.(e, n, a);
      if (y && y instanceof Promise) y.then((v) => g(v === void 0 ? void 0 : v), h);
      i.signal.addEventListener("abort", () => {
        if (!r.ignoreFetchAbort || r.allowStaleOnFetchAbort) {
          if (g(void 0), r.allowStaleOnFetchAbort) g = (v) => c(v, true);
        }
      });
    };
    if (r.status) r.status.fetchDispatched = true;
    let f = new Promise(p).then(c, u), m = Object.assign(f, { __abortController: i, __staleWhileFetching: n, __returned: void 0 });
    if (t === void 0) this.set(e, m, { ...a.options, status: void 0 }), t = this.#o.get(e);
    else this.#e[t] = m;
    return m;
  }
  #t(e) {
    if (!this.#T) return false;
    let t = e;
    return !!t && t instanceof Promise && t.hasOwnProperty("__staleWhileFetching") && t.__abortController instanceof og;
  }
  async fetch(e, t = {}) {
    let { allowStale: r = this.allowStale, updateAgeOnGet: o = this.updateAgeOnGet, noDeleteOnStaleGet: n = this.noDeleteOnStaleGet, ttl: i = this.ttl, noDisposeOnSet: s = this.noDisposeOnSet, size: a = 0, sizeCalculation: c = this.sizeCalculation, noUpdateTTL: u = this.noUpdateTTL, noDeleteOnFetchRejection: d = this.noDeleteOnFetchRejection, allowStaleOnFetchRejection: p = this.allowStaleOnFetchRejection, ignoreFetchAbort: f = this.ignoreFetchAbort, allowStaleOnFetchAbort: m = this.allowStaleOnFetchAbort, context: g, forceRefresh: h = false, status: y, signal: v } = t;
    if (!this.#T) {
      if (y) y.fetch = "get";
      return this.get(e, { allowStale: r, updateAgeOnGet: o, noDeleteOnStaleGet: n, status: y });
    }
    let x = { allowStale: r, updateAgeOnGet: o, noDeleteOnStaleGet: n, ttl: i, noDisposeOnSet: s, size: a, sizeCalculation: c, noUpdateTTL: u, noDeleteOnFetchRejection: d, allowStaleOnFetchRejection: p, allowStaleOnFetchAbort: m, ignoreFetchAbort: f, status: y, signal: v }, w = this.#o.get(e);
    if (w === void 0) {
      if (y) y.fetch = "miss";
      let A = this.#M(e, w, x, g);
      return A.__returned = A;
    } else {
      let A = this.#e[w];
      if (this.#t(A)) {
        let Lt = r && A.__staleWhileFetching !== void 0;
        if (y) {
          if (y.fetch = "inflight", Lt) y.returnedStale = true;
        }
        return Lt ? A.__staleWhileFetching : A.__returned = A;
      }
      let U = this.#m(w);
      if (!h && !U) {
        if (y) y.fetch = "hit";
        if (this.#R(w), o) this.#P(w);
        if (y) this.#E(y, w);
        return A;
      }
      let se = this.#M(e, w, x, g), Ye = se.__staleWhileFetching !== void 0 && r;
      if (y) {
        if (y.fetch = U ? "stale" : "refresh", Ye && U) y.returnedStale = true;
      }
      return Ye ? se.__staleWhileFetching : se.__returned = se;
    }
  }
  async forceFetch(e, t = {}) {
    let r = await this.fetch(e, t);
    if (r === void 0) throw Error("fetch() returned undefined");
    return r;
  }
  memo(e, t = {}) {
    let r = this.#O;
    if (!r) throw Error("no memoMethod provided to constructor");
    let { context: o, forceRefresh: n, ...i } = t, s = this.get(e, i);
    if (!n && s !== void 0) return s;
    let a = r(e, s, { options: i, context: o });
    return this.set(e, a, i), a;
  }
  get(e, t = {}) {
    let { allowStale: r = this.allowStale, updateAgeOnGet: o = this.updateAgeOnGet, noDeleteOnStaleGet: n = this.noDeleteOnStaleGet, status: i } = t, s = this.#o.get(e);
    if (s !== void 0) {
      let a = this.#e[s], c = this.#t(a);
      if (i) this.#E(i, s);
      if (this.#m(s)) {
        if (i) i.get = "stale";
        if (!c) {
          if (!n) this.#k(e, "expire");
          if (i && r) i.returnedStale = true;
          return r ? a : void 0;
        } else {
          if (i && r && a.__staleWhileFetching !== void 0) i.returnedStale = true;
          return r ? a.__staleWhileFetching : void 0;
        }
      } else {
        if (i) i.get = "hit";
        if (c) return a.__staleWhileFetching;
        if (this.#R(s), o) this.#P(s);
        return a;
      }
    } else if (i) i.get = "miss";
  }
  #z(e, t) {
    this.#p[t] = e, this.#l[e] = t;
  }
  #R(e) {
    if (e !== this.#s) {
      if (e === this.#c) this.#c = this.#l[e];
      else this.#z(this.#p[e], this.#l[e]);
      this.#z(this.#s, e), this.#s = e;
    }
  }
  delete(e) {
    return this.#k(e, "delete");
  }
  #k(e, t) {
    let r = false;
    if (this.#i !== 0) {
      let o = this.#o.get(e);
      if (o !== void 0) if (r = true, this.#i === 1) this.#L(t);
      else {
        this.#I(o);
        let n = this.#e[o];
        if (this.#t(n)) n.__abortController.abort(Error("deleted"));
        else if (this.#S || this.#u) {
          if (this.#S) this.#g?.(n, e, t);
          if (this.#u) this.#a?.push([n, e, t]);
        }
        if (this.#o.delete(e), this.#r[o] = void 0, this.#e[o] = void 0, o === this.#s) this.#s = this.#p[o];
        else if (o === this.#c) this.#c = this.#l[o];
        else {
          let i = this.#p[o];
          this.#l[i] = this.#l[o];
          let s = this.#l[o];
          this.#p[s] = this.#p[o];
        }
        this.#i--, this.#b.push(o);
      }
    }
    if (this.#u && this.#a?.length) {
      let o = this.#a, n;
      while (n = o?.shift()) this.#h?.(...n);
    }
    return r;
  }
  clear() {
    return this.#L("delete");
  }
  #L(e) {
    for (let t of this.#w({ allowStale: true })) {
      let r = this.#e[t];
      if (this.#t(r)) r.__abortController.abort(Error("deleted"));
      else {
        let o = this.#r[t];
        if (this.#S) this.#g?.(r, o, e);
        if (this.#u) this.#a?.push([r, o, e]);
      }
    }
    if (this.#o.clear(), this.#e.fill(void 0), this.#r.fill(void 0), this.#f && this.#v) this.#f.fill(0), this.#v.fill(0);
    if (this.#_) this.#_.fill(0);
    if (this.#c = 0, this.#s = 0, this.#b.length = 0, this.#y = 0, this.#i = 0, this.#u && this.#a) {
      let t = this.#a, r;
      while (r = t?.shift()) this.#h?.(...r);
    }
  }
};
function ZN(e, t, r = 100) {
  let o = new ig({ max: r }), n = (...i) => {
    let s = t(...i), a = o.get(s);
    if (a !== void 0) return a;
    let c = e(...i);
    return o.set(s, c), c;
  };
  return n.cache = { clear: () => o.clear(), size: () => o.size, delete: (i) => o.delete(i), get: (i) => o.peek(i), has: (i) => o.has(i) }, n;
}
var cee = 8192;
function WN(e, t) {
  try {
    return { ok: true, value: JSON.parse(rg(e)) };
  } catch (r) {
    if (t) ng(r);
    return { ok: false };
  }
}
var VN = ZN(WN, (e) => e, 50);
var Os = Object.assign(function(t, r = true) {
  if (!t) return null;
  let o = t.length > cee ? WN(t, r) : VN(t, r);
  return o.ok ? o.value : null;
}, { cache: VN.cache });
var Ho = Ce(() => {
  try {
    if (process.platform === "darwin") return "macos";
    if (process.platform === "win32") return "windows";
    if (process.platform === "linux") {
      if (process.env.WSL_DISTRO_NAME || process.env.WSL_INTEROP) return "wsl";
      try {
        let e = Be().readFileSync("/proc/version", { encoding: "utf8" });
        if (e.toLowerCase().includes("microsoft") || e.toLowerCase().includes("wsl")) return "wsl";
      } catch (e) {
        ee(`Failed to read /proc/version for WSL detection: ${e}`, { level: "error" });
      }
      return "linux";
    }
    return "unknown";
  } catch (e) {
    return ng(e), "unknown";
  }
});
var cRe = Ce(() => {
  if (process.platform !== "linux") return;
  try {
    let e = Be().readFileSync("/proc/version", { encoding: "utf8" }), t = e.match(/WSL(\d+)/i);
    if (t && t[1]) return t[1];
    if (e.toLowerCase().includes("microsoft")) return "1";
    return;
  } catch (e) {
    ee(`Failed to read /proc/version for WSL detection: ${e}`, { level: "error" });
    return;
  }
});
var lRe = Ce(async () => {
  if (process.platform !== "linux") return;
  let e = { linuxKernel: KN() };
  try {
    let t = await lee("/etc/os-release", "utf8");
    for (let r of t.split(`
`)) {
      let o = r.match(/^(ID|VERSION_ID)=(.*)$/);
      if (o && o[1] && o[2]) {
        let n = o[2].replace(/^"|"$/g, "");
        if (o[1] === "ID") e.linuxDistroId = n;
        else e.linuxDistroVersion = n;
      }
    }
  } catch {
  }
  return e;
});
var uRe = Ce(() => {
  if (process.platform !== "darwin") return;
  let t = KN().match(/^(\d+)\./);
  if (!t || !t[1]) return;
  return parseInt(t[1], 10) - 9;
});
var qo = Ce(function() {
  switch (Ho()) {
    case "macos":
      return "/Library/Application Support/ClaudeCode";
    case "windows":
      return "C:\\Program Files\\ClaudeCode";
    default:
      return "/etc/claude-code";
  }
});
var gRe = Ce(function() {
  return uee(qo(), "managed-settings.d");
});
function dee(e, t, r) {
  if (r !== void 0 && !dn(e[t], r) || r === void 0 && !(t in e)) wi(e, t, r);
}
var tu = dee;
function pee(e) {
  return function(t, r, o) {
    var n = -1, i = Object(t), s = o(t), a = s.length;
    while (a--) {
      var c = s[e ? a : ++n];
      if (r(i[c], c, i) === false) break;
    }
    return t;
  };
}
var GN = pee;
var fee = GN();
var JN = fee;
function mee(e) {
  return Kt(e) && Ei(e);
}
var XN = mee;
var gee = "[object Object]";
var hee = Function.prototype;
var yee = Object.prototype;
var YN = hee.toString;
var bee = yee.hasOwnProperty;
var _ee = YN.call(Object);
function vee(e) {
  if (!Kt(e) || wr(e) != gee) return false;
  var t = fd(e);
  if (t === null) return true;
  var r = bee.call(t, "constructor") && t.constructor;
  return typeof r == "function" && r instanceof r && YN.call(r) == _ee;
}
var QN = vee;
function See(e, t) {
  if (t === "constructor" && typeof e[t] === "function") return;
  if (t == "__proto__") return;
  return e[t];
}
var ru = See;
function xee(e) {
  return bE(e, ud(e));
}
var ej = xee;
function wee(e, t, r, o, n, i, s) {
  var a = ru(e, r), c = ru(t, r), u = s.get(c);
  if (u) {
    tu(e, r, u);
    return;
  }
  var d = i ? i(a, c, r + "", e, t, s) : void 0, p = d === void 0;
  if (p) {
    var f = dt(c), m = !f && Fa(c), g = !f && !m && cd(c);
    if (d = c, f || m || g) if (dt(a)) d = a;
    else if (XN(a)) d = jE(a);
    else if (m) p = false, d = vh(c, true);
    else if (g) p = false, d = LE(c, true);
    else d = [];
    else if (QN(c) || Lr(c)) {
      if (d = a, Lr(a)) d = ej(a);
      else if (!Qe(a) || Yo(a)) d = HE(c);
    } else p = false;
  }
  if (p) s.set(c, d), n(d, c, o, i, s), s.delete(c);
  tu(e, r, d);
}
var tj = wee;
function rj(e, t, r, o, n) {
  if (e === t) return;
  JN(t, function(i, s) {
    if (n || (n = new yE()), Qe(i)) tj(e, t, s, r, rj, o, n);
    else {
      var a = o ? o(ru(e, s), i, s + "", e, t, n) : void 0;
      if (a === void 0) a = i;
      tu(e, s, a);
    }
  }, ud);
}
var nj = rj;
function kee(e, t, r) {
  switch (r.length) {
    case 0:
      return e.call(t);
    case 1:
      return e.call(t, r[0]);
    case 2:
      return e.call(t, r[0], r[1]);
    case 3:
      return e.call(t, r[0], r[1], r[2]);
  }
  return e.apply(t, r);
}
var oj = kee;
var ij = Math.max;
function Eee(e, t, r) {
  return t = ij(t === void 0 ? e.length - 1 : t, 0), function() {
    var o = arguments, n = -1, i = ij(o.length - t, 0), s = Array(i);
    while (++n < i) s[n] = o[t + n];
    n = -1;
    var a = Array(t + 1);
    while (++n < t) a[n] = o[n];
    return a[t] = r(s), oj(e, this, a);
  };
}
var sg = Eee;
function Tee(e) {
  return function() {
    return e;
  };
}
var sj = Tee;
var Pee = !xi ? md : function(e, t) {
  return xi(e, "toString", { configurable: true, enumerable: false, value: sj(t), writable: true });
};
var aj = Pee;
var Iee = 800;
var Ree = 16;
var $ee = Date.now;
function Oee(e) {
  var t = 0, r = 0;
  return function() {
    var o = $ee(), n = Ree - (o - r);
    if (r = o, n > 0) {
      if (++t >= Iee) return arguments[0];
    } else t = 0;
    return e.apply(void 0, arguments);
  };
}
var cj = Oee;
var Aee = cj(aj);
var ag = Aee;
function Cee(e, t) {
  return ag(sg(e, t, md), e + "");
}
var lj = Cee;
function Mee(e, t, r) {
  if (!Qe(r)) return false;
  var o = typeof t;
  if (o == "number" ? Ei(r) && vn(t, r.length) : o == "string" && t in r) return dn(r[t], e);
  return false;
}
var uj = Mee;
function Dee(e) {
  return lj(function(t, r) {
    var o = -1, n = r.length, i = n > 1 ? r[n - 1] : void 0, s = n > 2 ? r[2] : void 0;
    if (i = e.length > 3 && typeof i == "function" ? (n--, i) : void 0, s && uj(r[0], r[1], s)) i = n < 3 ? void 0 : i, n = 1;
    t = Object(t);
    while (++o < n) {
      var a = r[o];
      if (a) e(t, a, o, i);
    }
    return t;
  });
}
var dj = Dee;
var Nee = dj(function(e, t, r, o) {
  nj(e, t, r, o);
});
function jee(e, t, r, o) {
  if (!Qe(e)) return e;
  t = Sn(t, e);
  var n = -1, i = t.length, s = i - 1, a = e;
  while (a != null && ++n < i) {
    var c = Pi(t[n]), u = r;
    if (c === "__proto__" || c === "constructor" || c === "prototype") return e;
    if (n != s) {
      var d = a[c];
      if (u = o ? o(d, c, a) : void 0, u === void 0) u = Qe(d) ? d : vn(t[n + 1]) ? [] : {};
    }
    rd(a, c, u), a = a[c];
  }
  return e;
}
var pj = jee;
function Uee(e, t, r) {
  var o = -1, n = t.length, i = {};
  while (++o < n) {
    var s = t[o], a = QE(e, s);
    if (r(a, s)) pj(i, Sn(s, e), a);
  }
  return i;
}
var fj = Uee;
function zee(e, t) {
  return fj(e, t, function(r, o) {
    return rT(e, o);
  });
}
var mj = zee;
var gj = Ht ? Ht.isConcatSpreadable : void 0;
function Lee(e) {
  return dt(e) || Lr(e) || !!(gj && e && e[gj]);
}
var hj = Lee;
function yj(e, t, r, o, n) {
  var i = -1, s = e.length;
  r || (r = hj), n || (n = []);
  while (++i < s) {
    var a = e[i];
    if (t > 0 && r(a)) if (t > 1) yj(a, t - 1, r, o, n);
    else UE(n, a);
    else if (!o) n[n.length] = a;
  }
  return n;
}
var bj = yj;
function Fee(e) {
  var t = e == null ? 0 : e.length;
  return t ? bj(e, 1) : [];
}
var _j = Fee;
function Bee(e) {
  return ag(sg(e, void 0, _j), e + "");
}
var vj = Bee;
var Hee = vj(function(e, t) {
  return e == null ? {} : mj(e, t);
});
var Jee = P(() => l.object({ allowedDomains: l.array(l.string()).optional(), deniedDomains: l.array(l.string()).optional().describe("Domains that are always blocked, even if matched by allowedDomains. Supports the same wildcard syntax as allowedDomains. Merged from all settings sources regardless of allowManagedDomainsOnly."), allowManagedDomainsOnly: l.boolean().optional().describe("When true (and set in managed settings), only allowedDomains and WebFetch(domain:...) allow rules from managed settings are respected. User, project, local, and flag settings domains are ignored. Denied domains are still respected from all sources."), allowUnixSockets: l.array(l.string()).optional().describe("macOS only: Unix socket paths to allow. Ignored on Linux (seccomp cannot filter by path)."), allowAllUnixSockets: l.boolean().optional().describe("If true, allow all Unix sockets (disables blocking on both platforms)."), allowLocalBinding: l.boolean().optional(), allowMachLookup: l.array(l.string().refine((e) => !(e.endsWith("*") ? e.slice(0, -1) : e).includes("*"), { message: 'Wildcards are only allowed as a single trailing "*" (e.g., "com.example.*" or "*" for all services).' })).optional().describe('macOS only: Additional XPC/Mach service names to allow looking up. Supports trailing-wildcard prefix matching (e.g., "com.apple.coresimulator.*"). Needed for tools that communicate via XPC such as the iOS Simulator or Playwright.'), httpProxyPort: l.number().optional(), socksProxyPort: l.number().optional(), tlsTerminate: l.object({ caCertPath: l.string().min(1).optional(), caKeyPath: l.string().min(1).optional() }).optional().describe("[EXPERIMENTAL] Enable in-process TLS termination so the per-request filter can see HTTPS request bodies. Provide a CA cert+key, or omit both to have sandbox-runtime generate an ephemeral one for the session.") }).optional());
var Xee = P(() => l.object({ allowWrite: l.array(l.string()).optional().describe("Additional paths to allow writing within the sandbox. Merged with paths from Edit(...) allow permission rules."), denyWrite: l.array(l.string()).optional().describe("Additional paths to deny writing within the sandbox. Merged with paths from Edit(...) deny permission rules."), denyRead: l.array(l.string()).optional().describe("Additional paths to deny reading within the sandbox. Merged with paths from Read(...) deny permission rules."), allowRead: l.array(l.string()).optional().describe("Paths to re-allow reading within denyRead regions. Takes precedence over denyRead for matching paths."), allowManagedReadPathsOnly: l.boolean().optional().describe("When true (set in managed settings), only allowRead paths from policySettings are used.") }).optional());
var $j = P(() => l.object({ enabled: l.boolean().optional(), failIfUnavailable: l.boolean().optional().describe("Exit with an error at startup if sandbox.enabled is true but the sandbox cannot start (missing dependencies or unsupported platform). When false (default), a warning is shown and commands run unsandboxed. Intended for managed-settings deployments that require sandboxing as a hard gate."), autoAllowBashIfSandboxed: l.boolean().optional(), allowUnsandboxedCommands: l.boolean().optional().describe("Allow commands to run outside the sandbox via the dangerouslyDisableSandbox parameter. When false, the dangerouslyDisableSandbox parameter is completely ignored and all commands must run sandboxed. Default: true."), network: Jee(), filesystem: Xee(), ignoreViolations: l.record(l.string(), l.array(l.string())).optional(), enableWeakerNestedSandbox: l.boolean().optional(), enableWeakerNetworkIsolation: l.boolean().optional().describe("macOS only: Allow access to com.apple.trustd.agent in the sandbox. Needed for Go-based CLI tools (gh, gcloud, terraform, etc.) to verify TLS certificates when using httpProxyPort with a MITM proxy and custom CA. **Reduces security** \u2014 opens a potential data exfiltration vector through the trustd service. Default: false"), excludedCommands: l.array(l.string()).optional(), ripgrep: l.object({ command: l.string(), args: l.array(l.string()).optional() }).optional().describe("Custom ripgrep configuration for bundled ripgrep support"), bwrapPath: l.preprocess((e) => typeof e === "string" && Rj(e) ? e : void 0, l.string()).optional().catch(void 0).describe("Linux/WSL only: Absolute path to the bwrap (bubblewrap) binary. Overrides auto-detection via PATH. Only honored from admin-controlled managed settings."), socatPath: l.preprocess((e) => typeof e === "string" && Rj(e) ? e : void 0, l.string()).optional().catch(void 0).describe("Linux/WSL only: Absolute path to the socat binary used for the sandbox network proxy. Overrides auto-detection via PATH. Only honored from admin-controlled managed settings.") }).passthrough());
var Oj = ["auto", "iterm2", "iterm2_with_bell", "terminal_bell", "kitty", "ghostty", "notifications_disabled"];
var Aj = ["normal", "vim"];
var Cj = ["auto", "tmux", "in-process"];
var Yee = ["dark", "light", "light-daltonized", "dark-daltonized", "light-ansi", "dark-ansi"];
var Mj = ["auto", ...Yee];
var E$e = Ho() === "macos" ? "\u23FA" : "\u25CF";
var iu = ["acceptEdits", "auto", "bypassPermissions", "default", "dontAsk", "plan"];
var Qee = [...iu, "bubble"];
var Dj = Qee;
var C$e = P(() => Vv.enum(Dj));
var M$e = P(() => Vv.enum(iu));
var Nj = ["bash", "powershell"];
var su = P(() => l.string().optional().describe('Permission rule syntax to filter when this hook runs (e.g., "Bash(git *)"). Only runs if the tool call matches the pattern. Avoids spawning hooks for non-matching commands.'));
function ete() {
  let e = l.object({ type: l.literal("command").describe("Shell command hook type"), command: l.string().describe("Shell command to execute"), args: l.array(l.string()).optional().describe("Argument list for exec form. When present, `command` is resolved as an executable and spawned directly with these arguments \u2014 no shell. Path placeholders like ${CLAUDE_PLUGIN_ROOT} are substituted per-element as plain strings, so paths with quotes, $, or backticks never reach a shell parser. When absent, `command` runs through a shell (bash on POSIX, PowerShell on Windows without Git Bash)."), if: su(), shell: l.enum(Nj).optional().describe("Shell interpreter. 'bash' uses your $SHELL (bash/zsh/sh); 'powershell' uses pwsh. Defaults to bash (powershell on Windows without Git Bash)."), timeout: l.number().positive().optional().describe("Timeout in seconds for this specific command"), statusMessage: l.string().optional().describe("Custom status message to display in spinner while hook runs"), once: l.boolean().optional().describe("If true, hook runs once and is removed after execution"), async: l.boolean().optional().describe("If true, hook runs in background without blocking"), asyncRewake: l.boolean().optional().describe("If true, hook runs in background and wakes the model on exit code 2 (blocking error). Implies async."), rewakeMessage: l.string().min(1).optional().describe("@internal Custom prefix for the system-reminder shown to the model when an asyncRewake hook exits with code 2. The hook output is appended after this prefix."), rewakeSummary: l.string().min(1).optional().describe('@internal One-line summary shown to the user in the terminal when an asyncRewake hook exits with code 2. Defaults to "Stop hook feedback".') }), t = l.object({ type: l.literal("prompt").describe("LLM prompt hook type"), prompt: l.string().describe("Prompt to evaluate with LLM. Use $ARGUMENTS placeholder for hook input JSON."), if: su(), timeout: l.number().positive().optional().describe("Timeout in seconds for this specific prompt evaluation"), model: l.string().optional().describe('Model to use for this prompt hook (e.g., "claude-sonnet-4-6"). If not specified, uses the default small fast model.'), continueOnBlock: l.boolean().optional().describe(`Sets the continue value for the decision:"block" produced when ok is false. Default false (turn ends). Whether continue:true lets the turn proceed depends on the event's decision:"block" semantics. On PostToolUse, the reason is fed back to Claude and the turn continues.`), statusMessage: l.string().optional().describe("Custom status message to display in spinner while hook runs"), once: l.boolean().optional().describe("If true, hook runs once and is removed after execution") }), r = l.object({ type: l.literal("mcp_tool").describe("MCP tool hook type"), server: l.string().describe("Name of an already-configured MCP server to invoke"), tool: l.string().describe("Name of the tool on that server to call"), input: l.record(l.string(), l.unknown()).optional().describe('Arguments passed to the MCP tool. String values support ${path} interpolation from the hook input JSON (e.g. "${tool_input.file_path}").'), if: su(), timeout: l.number().positive().optional().describe("Timeout in seconds for this specific tool call"), statusMessage: l.string().optional().describe("Custom status message to display in spinner while hook runs"), once: l.boolean().optional().describe("If true, hook runs once and is removed after execution") }), o = l.object({ type: l.literal("http").describe("HTTP hook type"), url: l.string().url().describe("URL to POST the hook input JSON to"), if: su(), timeout: l.number().positive().optional().describe("Timeout in seconds for this specific request"), headers: l.record(l.string(), l.string()).optional().describe('Additional headers to include in the request. Values may reference environment variables using $VAR_NAME or ${VAR_NAME} syntax (e.g., "Authorization": "Bearer $MY_TOKEN"). Only variables listed in allowedEnvVars will be interpolated.'), allowedEnvVars: l.array(l.string()).optional().describe("Explicit list of environment variable names that may be interpolated in header values. Only variables listed here will be resolved; all other $VAR references are left as empty strings. Required for env var interpolation to work."), statusMessage: l.string().optional().describe("Custom status message to display in spinner while hook runs"), once: l.boolean().optional().describe("If true, hook runs once and is removed after execution") }), n = l.object({ type: l.literal("agent").describe("Agentic verifier hook type"), prompt: l.string().describe('Prompt describing what to verify (e.g. "Verify that unit tests ran and passed."). Use $ARGUMENTS placeholder for hook input JSON.'), if: su(), timeout: l.number().positive().optional().describe("Timeout in seconds for agent execution (default 60)"), model: l.string().optional().describe('Model to use for this agent hook (e.g., "claude-sonnet-4-6"). If not specified, uses Haiku.'), statusMessage: l.string().optional().describe("Custom status message to display in spinner while hook runs"), once: l.boolean().optional().describe("If true, hook runs once and is removed after execution") });
  return { BashCommandHookSchema: e, PromptHookSchema: t, HttpHookSchema: o, AgentHookSchema: n, McpToolHookSchema: r };
}
var jj = P(() => {
  let { BashCommandHookSchema: e, PromptHookSchema: t, AgentHookSchema: r, HttpHookSchema: o, McpToolHookSchema: n } = ete();
  return l.discriminatedUnion("type", [e, t, r, o, n]);
});
var Uj = P(() => l.object({ matcher: l.string().optional().describe('String pattern to match (e.g. tool names like "Write")'), hooks: l.array(jj()).describe("List of hooks to execute when the matcher matches") }));
var Zo = P(() => l.partialRecord(l.enum(Xo), l.array(Uj())));
var q$e = P(() => l.enum(["local", "user", "project", "dynamic", "enterprise", "claudeai", "managed", "agent"]));
var Z$e = P(() => l.enum(["stdio", "sse", "sse-ide", "http", "ws", "sdk"]));
var Cs = P(() => l.literal("comms").optional().catch(void 0));
var Gn = P(() => l.number().int().positive());
var tte = P(() => l.object({ type: l.literal("stdio").optional(), command: l.string().min(1, "Command cannot be empty"), args: l.array(l.string()).default([]), env: l.record(l.string(), l.string()).optional(), timeout: Gn().optional(), alwaysLoad: l.boolean().optional(), role: Cs() }));
var rte = P(() => l.boolean());
var zj = P(() => l.object({ clientId: l.string().optional(), callbackPort: l.number().int().positive().optional(), authServerMetadataUrl: l.string().url().startsWith("https://", { message: "authServerMetadataUrl must use https://" }).optional(), scopes: l.string().min(1).optional(), xaa: rte().optional() }));
var nte = P(() => l.object({ type: l.literal("sse"), url: l.string(), headers: l.record(l.string(), l.string()).optional(), headersHelper: l.string().optional(), oauth: zj().optional(), timeout: Gn().optional(), alwaysLoad: l.boolean().optional(), role: Cs(), toolPermissions: l.record(l.string(), jx()).optional() }));
var ote = P(() => l.object({ type: l.literal("sse-ide"), url: l.string(), ideName: l.string(), ideRunningInWindows: l.boolean().optional(), timeout: Gn().optional(), alwaysLoad: l.boolean().optional(), role: Cs() }));
var ite = P(() => l.object({ type: l.literal("ws-ide"), url: l.string(), ideName: l.string(), authToken: l.string().optional(), ideRunningInWindows: l.boolean().optional(), timeout: Gn().optional(), alwaysLoad: l.boolean().optional(), role: Cs() }));
var ste = P(() => l.object({ type: l.enum(["http", "streamable-http"]).transform(() => "http"), url: l.string(), headers: l.record(l.string(), l.string()).optional(), headersHelper: l.string().optional(), oauth: zj().optional(), timeout: Gn().optional(), alwaysLoad: l.boolean().optional(), role: Cs(), toolPermissions: l.record(l.string(), jx()).optional() }));
var ate = P(() => l.object({ type: l.literal("ws"), url: l.string(), headers: l.record(l.string(), l.string()).optional(), headersHelper: l.string().optional(), timeout: Gn().optional(), alwaysLoad: l.boolean().optional(), role: Cs() }));
var cte = P(() => l.object({ type: l.literal("sdk"), name: l.string(), timeout: Gn().optional(), alwaysLoad: l.boolean().optional() }));
var jx = P(() => l.enum(["allow", "ask", "blocked"]));
var lte = P(() => l.object({ type: l.literal("claudeai-proxy"), url: l.string(), id: l.string(), displayName: l.string().optional(), timeout: Gn().optional(), alwaysLoad: l.boolean().optional(), toolPermissions: l.record(l.string(), jx()).optional(), stateless: l.boolean().optional(), cachedInitResponse: l.record(l.string(), l.unknown()).nullish() }));
var ug = P(() => l.union([tte(), nte(), ote(), ite(), ste(), ate(), cte(), lte()]));
var V$e = P(() => l.object({ mcpServers: l.record(l.string(), ug()) }));
var ute = /* @__PURE__ */ new Set(["claude-community", "claude-plugins-community"]);
var dte = /* @__PURE__ */ new Set(["claude-code-marketplace", "claude-code-plugins", "claude-plugins-official", "anthropic-marketplace", "anthropic-plugins", "agent-skills", "anthropic-agent-skills", "life-sciences", "knowledge-work-plugins", "claude-for-legal", "claude-for-financial-services", "financial-services-plugins"]);
var Hj = /* @__PURE__ */ new Set([...dte, ...ute]);
var pte = /(?:official[^a-z0-9]*(anthropic|claude)|(?:anthropic|claude)[^a-z0-9]*official|^(?:anthropic|claude)[^a-z0-9]*(marketplace|plugins|official))/i;
var fte = /[^\u0020-\u007E]/;
function mte(e) {
  if (Hj.has(e.toLowerCase())) return false;
  if (fte.test(e)) return true;
  return pte.test(e);
}
var vr = P(() => l.string().startsWith("./"));
var Vo = P(() => vr().endsWith(".json"));
var Lj = P(() => l.union([vr().refine((e) => e.endsWith(".mcpb") || e.endsWith(".dxt"), { message: "MCPB file path must end with .mcpb or .dxt" }).describe("Path to MCPB file relative to plugin root"), l.string().url().refine((e) => e.endsWith(".mcpb") || e.endsWith(".dxt"), { message: "MCPB URL must end with .mcpb or .dxt" }).describe("URL to MCPB file")]));
var zx = P(() => vr().endsWith(".md"));
var Lx = P(() => l.union([zx(), vr()]));
var qj = P(() => l.string().min(1, "Marketplace must have a name").refine((e) => !e.includes(" "), { message: 'Marketplace name cannot contain spaces. Use kebab-case (e.g., "my-marketplace")' }).refine((e) => !e.includes("/") && !e.includes("\\") && !e.includes("..") && e !== ".", { message: 'Marketplace name cannot contain path separators (/ or \\), ".." sequences, or be "."' }).refine((e) => !mte(e), { message: "Marketplace name impersonates an official Anthropic/Claude marketplace" }).refine((e) => e.toLowerCase() !== "inline", { message: 'Marketplace name "inline" is reserved for --plugin-dir session plugins' }).refine((e) => e.toLowerCase() !== "builtin", { message: 'Marketplace name "builtin" is reserved for built-in plugins' }).refine((e) => e.toLowerCase() !== "skills-dir", { message: 'Marketplace name "skills-dir" is reserved for plugins auto-loaded from .claude/skills/' }));
var Fx = P(() => l.object({ name: l.string().min(1, "Author name cannot be empty").describe("Display name of the plugin author or organization"), email: l.string().optional().describe("Contact email for support or feedback"), url: l.string().optional().describe("Website, GitHub profile, or organization URL") }));
var gte = P(() => l.object({ $schema: l.string().optional().describe("JSON Schema reference for editor autocomplete/validation; ignored at load time"), name: l.string().min(1, "Plugin name cannot be empty").refine((e) => !e.includes(" "), { message: 'Plugin name cannot contain spaces. Use kebab-case (e.g., "my-plugin")' }).describe("Unique identifier for the plugin, used for namespacing (prefer kebab-case)"), displayName: l.string().optional().describe('Human-readable name shown in UI (e.g., "GitHub Utils"). Falls back to `name` when omitted. Unlike `name`, may contain spaces and any casing; not used for namespacing or lookup.'), version: l.string().optional().describe("Semantic version (e.g., 1.2.3) following semver.org specification"), description: l.string().optional().describe("Brief, user-facing explanation of what the plugin provides"), author: Fx().optional().describe("Information about the plugin creator or maintainer"), homepage: l.string().url().optional().describe("Plugin homepage or documentation URL"), repository: l.string().optional().describe("Source code repository URL"), license: l.string().optional().describe("SPDX license identifier (e.g., MIT, Apache-2.0)"), keywords: l.array(l.string()).optional().describe("Tags for plugin discovery and categorization"), defaultEnabled: l.boolean().optional().describe("Whether the plugin starts enabled when the user has no explicit enabled/disabled setting for it (default: true). Explicit enabledPlugins values always win, and a plugin required by an enabled dependent is enabled regardless of this value."), dependencies: l.array(zte()).optional().describe(`Plugins that must be enabled for this plugin to function. Bare names (no "@marketplace") are resolved against the declaring plugin's own marketplace.`) }));
var eOe = P(() => l.object({ description: l.string().optional().describe("Brief, user-facing explanation of what these hooks provide"), hooks: l.lazy(() => Zo()).describe("The hooks provided by the plugin, in the same format as the one used for settings") }));
var hte = P(() => l.object({ hooks: l.union([Vo().describe("Path to file with additional hooks (in addition to those in hooks/hooks.json, if it exists), relative to the plugin root"), l.lazy(() => Zo()).describe("Additional hooks (in addition to those in hooks/hooks.json, if it exists)"), l.array(l.union([Vo().describe("Path to file with additional hooks (in addition to those in hooks/hooks.json, if it exists), relative to the plugin root"), l.lazy(() => Zo()).describe("Additional hooks (in addition to those in hooks/hooks.json, if it exists)")]))]) }));
var yte = P(() => l.object({ source: Lx().optional().describe("Path to command markdown file, relative to plugin root"), content: l.string().optional().describe("Inline markdown content for the command"), description: l.string().optional().describe("Command description override"), argumentHint: l.string().optional().describe('Hint for command arguments (e.g., "[file]")'), model: l.string().optional().describe("Default model for this command"), allowedTools: l.array(l.string()).optional().describe("Tools allowed when command runs") }).refine((e) => e.source && !e.content || !e.source && e.content, { message: 'Command must have either "source" (file path) or "content" (inline markdown), but not both' }));
var bte = P(() => l.object({ commands: l.union([Lx().describe("Path to a command file or skill directory, relative to the plugin root. When set, the commands/ directory is not auto-loaded \u2014 list its files here if you want both."), l.array(Lx().describe("Path to a command file or skill directory, relative to the plugin root. When set, the commands/ directory is not auto-loaded \u2014 list its files here if you want both.")).describe("List of command file or skill directory paths. When set, the commands/ directory is not auto-loaded."), l.record(l.string(), yte()).describe('Object mapping of command names to their metadata and source files. Command name becomes the slash command name (e.g., "about" \u2192 "/plugin:about")')]) }));
var _te = P(() => l.object({ agents: l.union([zx().describe("Path to an agent file, relative to the plugin root. When set, the agents/ directory is not auto-loaded \u2014 list its files here if you want both."), l.array(zx().describe("Path to an agent file, relative to the plugin root. When set, the agents/ directory is not auto-loaded \u2014 list its files here if you want both.")).describe("List of agent file paths. When set, the agents/ directory is not auto-loaded.")]) }));
var vte = P(() => l.object({ skills: l.union([vr().describe("Path to a skill directory, relative to the plugin root. Loaded in addition to the skills/ directory (except: for a marketplace entry whose source resolves to the marketplace root, declaring a specific subdirectory replaces the skills/ scan)."), l.array(vr().describe("Path to a skill directory, relative to the plugin root.")).describe("List of skill directory paths, loaded in addition to the skills/ directory (except: for a marketplace entry whose source resolves to the marketplace root, declaring specific subdirectories replaces the skills/ scan).")]) }));
var Zj = P(() => l.object({ outputStyles: l.union([vr().describe("Path to an output-styles directory or file, relative to the plugin root. When set, the output-styles/ directory is not auto-loaded \u2014 list its files here if you want both."), l.array(vr().describe("Path to an output-styles directory or file, relative to the plugin root. When set, the output-styles/ directory is not auto-loaded \u2014 list its files here if you want both.")).describe("List of output-style directory or file paths. When set, the output-styles/ directory is not auto-loaded.")]) }));
var Vj = P(() => l.object({ themes: l.union([vr().describe("Path to a themes directory or file, relative to the plugin root. When set, the themes/ directory is not auto-loaded \u2014 list its files here if you want both."), l.array(vr().describe("Path to a themes directory or file, relative to the plugin root. When set, the themes/ directory is not auto-loaded \u2014 list its files here if you want both.")).describe("List of theme directory or file paths. When set, the themes/ directory is not auto-loaded.")]) }));
var Ste = P(() => l.object({}));
var Fj = P(() => l.string().min(1));
var xte = P(() => l.string().min(2).refine((e) => e.startsWith("."), { message: 'File extensions must start with dot (e.g., ".ts", not "ts")' }));
var wte = P(() => l.object({ mcpServers: l.union([Vo().describe("MCP servers to include in the plugin (in addition to those in the .mcp.json file, if it exists)"), Lj().describe("Path or URL to MCPB file containing MCP server configuration"), l.record(l.string(), ug()).describe("MCP server configurations keyed by server name"), l.array(l.union([Vo().describe("Path to MCP servers configuration file"), Lj().describe("Path or URL to MCPB file"), l.record(l.string(), ug()).describe("Inline MCP server configurations")])).describe("Array of MCP server configurations (paths, MCPB files, or inline definitions)")]) }));
var Wj = P(() => l.object({ type: l.enum(["string", "number", "boolean", "directory", "file"]).describe("Type of the configuration value"), title: l.string().describe("Human-readable label shown in the config dialog"), description: l.string().describe("Help text shown beneath the field in the config dialog"), required: l.boolean().optional().describe("If true, validation fails when this field is empty"), default: l.union([l.string(), l.number(), l.boolean(), l.array(l.string())]).optional().describe("Default value used when the user provides nothing"), multiple: l.boolean().optional().describe("For string type: allow an array of strings"), sensitive: l.boolean().optional().describe("If true, masks dialog input and stores value in secure storage (keychain/credentials file) instead of settings.json"), min: l.number().optional().describe("Minimum value (number type only)"), max: l.number().optional().describe("Maximum value (number type only)") }).strict());
var kte = P(() => l.object({ userConfig: l.record(l.string().regex(/^[A-Za-z_]\w*$/, "Option keys must be valid identifiers (letters, digits, underscore; no leading digit) \u2014 they become CLAUDE_PLUGIN_OPTION_<KEY> env vars in hooks"), Wj()).optional().describe("User-configurable values this plugin needs. Prompted at enable time. Non-sensitive values saved to settings.json; sensitive values to secure storage. Available as ${user_config.KEY} in MCP/LSP server config, hook commands, and (non-sensitive only) skill/agent content. Keep sensitive value counts small.") }));
var Ete = P(() => l.object({ channels: l.array(l.object({ server: l.string().min(1).describe("Name of the MCP server this channel binds to. Must match a key in this plugin's mcpServers."), displayName: l.string().optional().describe('Human-readable name shown in the config dialog title (e.g., "Telegram"). Defaults to the server name.'), userConfig: l.record(l.string(), Wj()).optional().describe("Fields to prompt the user for when enabling this plugin in assistant mode. Saved values are substituted into ${user_config.KEY} references in the mcpServers env.") }).strict()).describe("Channels this plugin provides. Each entry declares an MCP server as a message channel and optionally specifies user configuration to prompt for at enable time.") }));
var Bj = P(() => l.strictObject({ command: l.string().min(1).refine((e) => {
  if (e.includes(" ") && !e.startsWith("/")) return false;
  return true;
}, { message: "Command should not contain spaces. Use args array for arguments." }).describe('Command to execute the LSP server (e.g., "typescript-language-server")'), args: l.array(Fj()).optional().describe("Command-line arguments to pass to the server"), extensionToLanguage: l.record(xte(), Fj()).refine((e) => Object.keys(e).length > 0, { message: "extensionToLanguage must have at least one mapping" }).describe("Mapping from file extension to LSP language ID. File extensions and languages are derived from this mapping."), transport: l.enum(["stdio", "socket"]).default("stdio").describe("Communication transport mechanism"), env: l.record(l.string(), l.string()).optional().describe("Environment variables to set when starting the server"), initializationOptions: l.unknown().optional().describe("Initialization options passed to the server during initialization"), settings: l.unknown().optional().describe("Settings passed to the server via workspace/didChangeConfiguration"), workspaceFolder: l.string().optional().describe("Workspace folder path to use for the server"), startupTimeout: l.number().int().positive().optional().describe("Maximum time to wait for server startup (milliseconds)"), shutdownTimeout: l.number().int().positive().optional().describe("Maximum time to wait for graceful shutdown (milliseconds)"), restartOnCrash: l.boolean().optional().describe("Whether to restart the server if it crashes"), maxRestarts: l.number().int().nonnegative().optional().describe("Maximum number of restart attempts before giving up"), diagnostics: l.boolean().optional().describe("Whether to push publishDiagnostics into the agent context after edits. Set to false to keep LSP navigation (goToDefinition, hover, etc.) but suppress automatic diagnostic injection. Defaults to true.") }));
var Tte = P(() => l.strictObject({ name: l.string().min(1).describe("Identifier for this monitor, unique within the plugin. Used to dedupe so re-arming (plugin reload, repeat skill invoke) does not spawn duplicates."), command: l.string().min(1).describe('Shell command to run as a persistent background monitor. Each stdout line is delivered to the model as a <task_notification> event; the process runs for the session lifetime. ${CLAUDE_PLUGIN_ROOT}, ${CLAUDE_PLUGIN_DATA}, ${CLAUDE_PROJECT_DIR}, ${user_config.*}, and ${ENV_VAR} are substituted. Runs in the session cwd \u2014 prefix with `cd "${CLAUDE_PLUGIN_ROOT}" && ` if the script needs its own directory.'), description: l.string().min(1).describe("Short human-readable description of what is being monitored (shown in task panel and notification summary)."), when: l.union([l.literal("always"), l.string().startsWith("on-skill-invoke:").refine((e) => e.length > 16, { message: "on-skill-invoke: must specify a skill name" })]).default("always").describe('Arm trigger. "always" arms at session start and on plugin reload. "on-skill-invoke:<skill>" arms the first time that skill is dispatched (via Skill tool or slash command).') }));
var Pte = P(() => l.array(Tte()).refine((e) => new Set(e.map((t) => t.name)).size === e.length, { message: "Monitor names must be unique within a plugin" }));
var Kj = P(() => l.object({ monitors: l.union([Vo().describe("Path to a JSON file containing the monitors array, relative to the plugin root"), Pte()]).describe("Background watch scripts the host arms as persistent Monitor tasks (unsandboxed, same trust tier as hooks) so plugins need not instruct the model to arm them. When omitted, monitors/monitors.json at the plugin root is loaded if present.") }));
var Ite = P(() => l.object({ lspServers: l.union([Vo().describe("Path to .lsp.json configuration file relative to plugin root"), l.record(l.string(), Bj()).describe("LSP server configurations keyed by server name"), l.array(l.union([Vo().describe("Path to LSP configuration file"), l.record(l.string(), Bj()).describe("Inline LSP server configurations")])).describe("Array of LSP server configurations (paths or inline definitions)")]) }));
var Gj = P(() => l.string().refine((e) => !e.includes("..") && !e.includes("//"), "Package name cannot contain path traversal patterns").refine((e) => {
  let t = /^@[a-z0-9][a-z0-9-._]*\/[a-z0-9][a-z0-9-._]*$/, r = /^[a-z0-9][a-z0-9-._]*$/;
  return t.test(e) || r.test(e);
}, "Invalid npm package name format"));
var Rte = P(() => l.object({ settings: l.record(l.string(), l.unknown()).optional().describe("Settings to merge into the user settings while this plugin is enabled. Only the documented allowlisted keys are applied.") }));
var $te = P(() => l.object({ experimental: l.preprocess((e) => typeof e === "object" && e !== null && !Array.isArray(e) ? e : void 0, l.object({ ...Vj().partial().shape, ...Kj().partial().shape, ...Zj().partial().shape, evals: l.union([l.string(), l.array(l.string())]).optional().describe("Path(s) to evaluation query files for `claude plugin eval`. Defaults to `evals/`.") }).optional().describe("Components whose manifest shape may change without a deprecation cycle. Move a key out of here once it is promoted to stable.")) }));
var Ote = P(() => l.object({ ...gte().shape, ...hte().partial().shape, ...bte().partial().shape, ..._te().partial().shape, ...vte().partial().shape, ...Zj().partial().shape, ...Vj().partial().shape, ...Ste().shape, ...Ete().partial().shape, ...wte().partial().shape, ...Ite().partial().shape, ...Kj().partial().shape, ...Rte().partial().shape, ...kte().partial().shape, ...$te().partial().shape }));
var au = P(() => l.discriminatedUnion("source", [l.object({ source: l.literal("url"), url: l.string().url().describe("Direct URL to marketplace.json file"), headers: l.record(l.string(), l.string()).optional().describe("Custom HTTP headers (e.g., for authentication)") }), l.object({ source: l.literal("github"), repo: l.string().describe("GitHub repository in owner/repo format"), ref: l.string().optional().describe('Git branch or tag to use (e.g., "main", "v1.0.0"). Defaults to repository default branch.'), path: l.string().optional().describe("Path to marketplace.json within repo (defaults to .claude-plugin/marketplace.json)"), sparsePaths: l.array(l.string()).optional().describe('Directories to include via git sparse-checkout (cone mode). Use for monorepos where the marketplace lives in a subdirectory. Example: [".claude-plugin", "plugins"]. If omitted, the full repository is cloned.'), skipLfs: l.boolean().optional().describe("Skip Git LFS smudge during clone and update (sets GIT_LFS_SKIP_SMUDGE=1) so LFS pointer files stay as pointers instead of downloading their content. Use for marketplaces hosted in repos with large LFS objects.") }), l.object({ source: l.literal("git"), url: l.string().describe("Full git repository URL"), ref: l.string().optional().describe('Git branch or tag to use (e.g., "main", "v1.0.0"). Defaults to repository default branch.'), path: l.string().optional().describe("Path to marketplace.json within repo (defaults to .claude-plugin/marketplace.json)"), sparsePaths: l.array(l.string()).optional().describe('Directories to include via git sparse-checkout (cone mode). Use for monorepos where the marketplace lives in a subdirectory. Example: [".claude-plugin", "plugins"]. If omitted, the full repository is cloned.'), skipLfs: l.boolean().optional().describe("Skip Git LFS smudge during clone and update (sets GIT_LFS_SKIP_SMUDGE=1) so LFS pointer files stay as pointers instead of downloading their content. Use for marketplaces hosted in repos with large LFS objects.") }), l.object({ source: l.literal("npm"), package: Gj().describe("NPM package containing marketplace.json") }), l.object({ source: l.literal("file"), path: l.string().describe("Local file path to marketplace.json") }), l.object({ source: l.literal("directory"), path: l.string().describe("Local directory containing .claude-plugin/marketplace.json") }), l.object({ source: l.literal("skills-dir") }).describe("Policy-list sentinel for the ~/.claude/skills/ auto-load (@skills-dir plugins). In strictKnownMarketplaces: opt the scan back IN (by default any allowlist blocks it). In blockedMarketplaces: turn the scan OFF without otherwise restricting marketplaces. Only meaningful in those two managed-settings lists (areLocalPluginDirsAllowedByPolicy); known_marketplaces.json / marketplace add etc. ignore it."), l.object({ source: l.literal("hostPattern"), hostPattern: l.string().describe('Regex pattern to match the host/domain extracted from any marketplace source type. For github sources, matches against "github.com". For git sources (SSH or HTTPS), extracts the hostname from the URL. Use in strictKnownMarketplaces to allow all marketplaces from a specific host (e.g., "^github\\.mycompany\\.com$").') }), l.object({ source: l.literal("pathPattern"), pathPattern: l.string().describe('Regex pattern matched against the .path field of file and directory sources. Use in strictKnownMarketplaces to allow filesystem-based marketplaces alongside hostPattern restrictions for network sources. Use ".*" to allow all filesystem paths, or a narrower pattern (e.g., "^/opt/approved/") to restrict to specific directories.') }), l.object({ source: l.literal("settings"), name: qj().refine((e) => !Hj.has(e.toLowerCase()), { message: "Reserved marketplace names cannot be used with settings sources. validateOfficialNameSource only accepts github/git sources from anthropics/* for these names; a settings source would be rejected after loadAndCacheMarketplace has already written to disk with cleanupNeeded=false." }).describe("Marketplace name. Must match the extraKnownMarketplaces key (enforced); the synthetic manifest is written under this name. Same validation as PluginMarketplaceSchema plus reserved-name rejection \u2014 validateOfficialNameSource runs after the disk write, too late to clean up."), plugins: l.array(Ate()).describe("Plugin entries declared inline in settings.json"), owner: Fx().optional() }).describe("Inline marketplace manifest defined directly in settings.json. The reconciler writes a synthetic marketplace.json to the cache; diffMarketplaces detects edits via isEqual on the stored source (the plugins array is inside this object, so edits surface as sourceChanged).")]));
var Ux = P(() => l.string().length(40).regex(/^[a-f0-9]{40}$/, "Must be a full 40-character lowercase git commit SHA"));
var Jj = P(() => l.union([l.preprocess((e) => e === "." ? "./" : e, vr()).describe("Path to the plugin root, relative to the marketplace root (the directory containing .claude-plugin/, not .claude-plugin/ itself)"), l.object({ source: l.literal("npm"), package: Gj().or(l.string().refine((e) => /^(?:file|https?|git(?:\+https?|\+ssh)?|ssh|github|gitlab|bitbucket):/i.test(e) || !e.includes(".."), 'Package reference cannot contain ".." path segments')).describe("Package name (or url, or local path, or anything else that can be passed to `npm` as a package)"), version: l.string().optional().describe("Specific version or version range (e.g., ^1.0.0, ~2.1.0)"), registry: l.string().url().optional().describe("Custom NPM registry URL (defaults to using system default, likely npmjs.org)") }).describe("NPM package as plugin source"), l.object({ source: l.literal("url"), url: l.string().describe("Full git repository URL (https:// or git@)"), ref: l.string().optional().describe('Git branch or tag to use (e.g., "main", "v1.0.0"). Defaults to repository default branch.'), sha: Ux().optional().describe("Specific commit SHA to use") }), l.object({ source: l.literal("github"), repo: l.string().describe("GitHub repository in owner/repo format"), ref: l.string().optional().describe('Git branch or tag to use (e.g., "main", "v1.0.0"). Defaults to repository default branch.'), sha: Ux().optional().describe("Specific commit SHA to use") }), l.object({ source: l.literal("git-subdir"), url: l.string().describe("Git repository: GitHub owner/repo shorthand, https://, or git@ URL"), path: l.string().min(1).describe('Subdirectory within the repo containing the plugin (e.g., "tools/claude-plugin"). Cloned sparsely using partial clone (--filter=tree:0) to minimize bandwidth for monorepos.'), ref: l.string().optional().describe('Git branch or tag to use (e.g., "main", "v1.0.0"). Defaults to repository default branch.'), sha: Ux().optional().describe("Specific commit SHA to use") }).describe("Plugin located in a subdirectory of a larger repository (monorepo). Only the specified subdirectory is materialized; the rest of the repo is not downloaded."), l.object({ source: l.literal("unsupported") }).describe('Placeholder for source types this Claude Code version does not recognize. Never authored by hand \u2014 PluginMarketplaceSchema rewrites unparseable sources to this so the entry remains in marketplace.plugins (detectDelistedPlugins must not see it as removed). Install attempts fail at cachePlugin with a clear "update Claude Code" message.')]));
var Ate = P(() => l.object({ name: l.string().min(1, "Plugin name cannot be empty").refine((e) => !e.includes(" "), { message: 'Plugin name cannot contain spaces. Use kebab-case (e.g., "my-plugin")' }).describe("Plugin name as it appears in the target repository"), source: Jj().describe("Where to fetch the plugin from. Must be a remote source \u2014 relative paths have no marketplace repository to resolve against."), description: l.string().optional(), version: l.string().optional(), strict: l.boolean().optional() }).refine((e) => typeof e.source !== "string", { message: 'Plugins in a settings-sourced marketplace must use remote sources (github, git-subdir, npm, url). Relative-path sources like "./foo" have no marketplace repository to resolve against.' }).refine((e) => typeof e.source === "string" || e.source.source !== "unsupported", { message: "source.source: 'unsupported' is a parse-time placeholder and cannot be authored. Use a remote source (github, git-subdir, npm, url)." }));
var Cte = P(() => l.object({ cli: l.array(l.string().max(64)).max(10).optional().describe('First command tokens (e.g. ["stripe"]) \u2014 exact match against commands run this session.'), hosts: l.array(l.string().max(128)).max(20).optional().describe('Hostnames (e.g. ["api.stripe.com"]) \u2014 exact, case-insensitive match against hostnames seen in https?:// URLs in bash commands run this session. Bare hostname only: lowercase, no scheme, no port, no path.'), filesRead: l.array(l.string().max(256)).max(10).optional().describe('Glob patterns (e.g. ["**/*.tf"]) \u2014 the plugin is relevant when a file Claude has read this session matches any pattern. Matched against read-file paths, forward-slash normalized, case-insensitive.'), manifestDeps: l.array(l.object({ file: l.string().max(256), pattern: l.string().max(256) })).max(10).optional().describe("Dependency declared in a package manifest. Each {file, pattern} is a pair of RegExp sources: `file` matches the manifest filename (package.json, go.mod, requirements.txt, \u2026); `pattern` matches the dependency declaration inside that file. Evaluated against files read this session."), cwd: l.array(l.string().max(256)).max(10).optional().describe(`Glob patterns (e.g. ["Engine/Source/Runtime/Renderer/**"]) \u2014 the plugin is relevant when the session's working directory is at or under a directory matching the pattern. Matched against the cwd both relative to the enclosing git repo root and as an absolute path, forward-slash normalized, case-insensitive. A bare directory (no glob characters) means "cwd is at or under this directory". Known at session start, so this signal can surface a suggestion before the first turn.`) }));
var Mte = P(() => l.object({ topic: l.string().max(64).optional().describe('What the user is working with when this plugin is relevant \u2014 fills "Working with {topic}?". Often the product name (e.g. "Stripe"); use a domain (e.g. "design") when the plugin name does not read naturally as a topic. Defaults to the plugin name with each hyphen-segment capitalized.'), signals: Cte().optional().describe("Matchers that determine when the plugin is relevant.") }));
var Dte = P(() => Ote().partial().extend({ name: l.string().min(1, "Plugin name cannot be empty").refine((e) => !e.includes(" "), { message: 'Plugin name cannot contain spaces. Use kebab-case (e.g., "my-plugin")' }).describe("Unique identifier matching the plugin name"), source: Jj().describe("Where to fetch the plugin from"), category: l.string().optional().describe('Category for organizing plugins (e.g., "productivity", "development")'), tags: l.array(l.string()).optional().describe("Tags for searchability and discovery"), strict: l.boolean().optional().default(true).describe("Require the plugin manifest to be present in the plugin folder. If false, the marketplace entry provides the manifest."), relevance: l.preprocess((e) => typeof e === "object" && e !== null && !Array.isArray(e) ? e : void 0, Mte().optional()).describe(`Declares when this plugin is relevant to the user's work. Consumed by the spinner tip ("Working with {topic}?"), session-start auto-suggest, and marketplace browse ranking.`) }));
var Nte = P(() => l.object({ name: l.string().min(1).refine((e) => !e.includes(" ")) }));
function jte(e) {
  let t = Dte();
  return e.flatMap((r, o) => {
    let n = t.safeParse(r);
    if (n.success) return [n.data];
    let i = Nte().safeParse(r).data?.name, s = n.error.issues.map((a) => `${a.path.join(".")}: ${a.message}`).join(", ");
    if (i) return ee(`Stubbing unparseable marketplace plugin entry (${i}): ${s}`, { level: "warn" }), [{ name: i, source: { source: "unsupported" }, strict: true }];
    return ee(`Dropping unparseable marketplace plugin entry (index ${o}): ${s}`, { level: "warn" }), [];
  });
}
var tOe = P(() => l.object({ $schema: l.string().optional().describe("JSON Schema reference for editor autocomplete/validation; ignored at load time"), name: qj(), version: l.string().optional().describe("Marketplace manifest version"), description: l.string().optional().describe("Human-readable description of this marketplace"), owner: Fx().describe("Marketplace maintainer or curator information"), plugins: l.array(l.unknown()).transform(jte).describe("Collection of available plugins in this marketplace"), forceRemoveDeletedPlugins: l.boolean().optional().describe("When true, plugins removed from this marketplace will be automatically uninstalled and flagged for users"), metadata: l.object({ pluginRoot: l.string().optional().describe("Base path for relative plugin sources"), version: l.string().optional().describe("Marketplace version"), description: l.string().optional().describe("Marketplace description") }).optional().describe("Optional marketplace metadata"), allowCrossMarketplaceDependenciesOn: l.array(l.string()).optional().describe("Marketplace names whose plugins may be auto-installed as dependencies. Only the root marketplace's allowlist applies \u2014 no transitive trust.") }));
var Xj = P(() => l.string().regex(/^[A-Za-z0-9][-A-Za-z0-9._]*@[A-Za-z0-9][-A-Za-z0-9._]*$/, "Plugin ID must be in format: plugin@marketplace"));
var Ute = /^[A-Za-z0-9][-A-Za-z0-9._]*(@[A-Za-z0-9][-A-Za-z0-9._]*)?(@\^[^@]*)?$/;
var zte = P(() => l.union([l.string().regex(Ute, "Dependency must be a plugin name, optionally qualified with @marketplace").transform((e) => e.replace(/@\^[^@]*$/, "")), l.object({ name: l.string().min(1).regex(/^[A-Za-z0-9][-A-Za-z0-9._]*$/), marketplace: l.string().min(1).regex(/^[A-Za-z0-9][-A-Za-z0-9._]*$/).optional() }).loose().transform((e) => e.marketplace ? `${e.name}@${e.marketplace}` : e.name)]));
var Lte = P(() => l.object({ version: l.string().describe("Currently installed version"), installedAt: l.string().describe("ISO 8601 timestamp of installation"), lastUpdated: l.string().optional().describe("ISO 8601 timestamp of last update"), installPath: l.string().describe("Absolute path to the installed plugin directory"), gitCommitSha: l.string().optional().describe("Git commit SHA for git-based plugins (for version tracking)"), resolvedVersion: l.string().optional().describe("Tag-derived semver this install resolved to (when fetched via a version constraint). Used by verifyAndDemote in preference to manifest.version, since the upstream may have forgotten to bump plugin.json."), auto: l.boolean().optional().describe("True when this plugin was pulled in as a dependency rather than installed explicitly. Auto-installed plugins are eligible for removal by the orphan sweep when nothing depends on them. Absent = manual (preserves pre-flag installs).") }));
var Fte = P(() => l.object({ version: l.literal(1).describe("Schema version 1"), plugins: l.record(Xj(), Lte()).describe("Map of plugin IDs to their installation metadata") }));
var Bte = P(() => l.enum(["managed", "user", "project", "local"]));
var Hte = P(() => l.object({ scope: Bte().describe("Installation scope"), projectPath: l.string().optional().describe("Project path (required for project/local scopes)"), installPath: l.string().describe("Absolute path to the versioned plugin directory"), version: l.string().optional().describe("Currently installed version"), installedAt: l.string().optional().describe("ISO 8601 timestamp of installation"), lastUpdated: l.string().optional().describe("ISO 8601 timestamp of last update"), gitCommitSha: l.string().optional().describe("Git commit SHA for git-based plugins"), resolvedVersion: l.string().optional().describe("Tag-derived semver this install resolved to"), auto: l.boolean().optional().describe("True when pulled in as a dependency. Eligible for orphan sweep.") }));
var qte = P(() => l.object({ version: l.literal(2).describe("Schema version 2"), plugins: l.record(Xj(), l.array(Hte())).describe("Map of plugin IDs to arrays of installation entries") }));
var rOe = P(() => l.union([Fte(), qte()]));
var Zte = P(() => l.object({ source: au().describe("Where to fetch the marketplace from"), installLocation: l.string().describe("Local cache path where marketplace manifest is stored"), lastUpdated: l.string().describe("ISO 8601 timestamp of last marketplace refresh"), autoUpdate: l.boolean().optional().describe("Whether to automatically update this marketplace and its installed plugins on startup") }));
var nOe = P(() => l.record(l.string(), Zte()));
var Vte = ["autoMode", "deepLink", "voice", "assistant", "briefView", "screenReader"];
var cu = {};
var dg = { autoMode: { buildGate: () => false, shape: () => cu, permissionsShape: () => cu, permissionModes: () => [] }, deepLink: { buildGate: () => true, shape: () => ({ disableDeepLinkRegistration: l.enum(["disable"]).optional().describe("Prevent claude-cli:// protocol handler registration with the OS") }) }, voice: { buildGate: () => false, shape: () => cu }, assistant: { buildGate: () => false, shape: () => cu }, briefView: { buildGate: () => true, shape: () => ({ defaultView: l.enum(["chat", "transcript"]).optional().describe("Default transcript view: chat (SendUserMessage checkpoints only) or transcript (full)") }) }, screenReader: { buildGate: () => false, shape: () => cu } };
function Bx() {
  return Vte.filter((e) => dg[e].buildGate());
}
function Yj(e) {
  let t = {};
  for (let r of e) t = { ...t, ...dg[r].shape() };
  return t;
}
function Qj(e) {
  let t = {};
  for (let r of e) t = { ...t, ...dg[r].permissionsShape?.() };
  return t;
}
function eU(e) {
  let t = [];
  for (let r of e) t.push(...dg[r].permissionModes?.() ?? []);
  return t;
}
function Hx(e) {
  let t = e.split("__"), [r, o, ...n] = t;
  if (r !== "mcp" || !o) return null;
  let i = n.length > 0 ? n.join("__") : void 0;
  return { serverName: o, toolName: i };
}
var tU = { Task: "Agent", KillShell: "TaskStop", KillBash: "TaskStop", AgentOutputTool: "TaskOutput", BashOutputTool: "TaskOutput", AgentOutput: "TaskOutput", BashOutput: "TaskOutput", ListPeers: "ListAgents", Brief: "SendUserMessage", ListMcpResources: "ListMcpResourcesTool", ReadMcpResource: "ReadMcpResourceTool" };
function Ms(e) {
  return Object.hasOwn(tU, e) ? tU[e] : e;
}
var rU = "workspace";
var mOe = `mcp__${rU}__bash`;
var gOe = `mcp__${rU}__web_fetch`;
function qx(e) {
  return e.includes("*");
}
function Wte(e) {
  return e.replaceAll("\\(", "(").replaceAll("\\)", ")").replaceAll("\\\\", "\\");
}
function nU(e) {
  let t = Kte(e, "(");
  if (t === -1) return { toolName: Ms(e) };
  let r = Gte(e, ")");
  if (r === -1 || r <= t) return { toolName: Ms(e) };
  if (r !== e.length - 1) return { toolName: Ms(e) };
  let o = e.substring(0, t), n = e.substring(t + 1, r);
  if (!o) return { toolName: Ms(e) };
  if (n === "" || n === "*") return { toolName: Ms(o) };
  let i = Wte(n);
  return { toolName: Ms(o), ruleContent: i };
}
function Kte(e, t) {
  for (let r = 0; r < e.length; r++) if (e[r] === t) {
    let o = 0, n = r - 1;
    while (n >= 0 && e[n] === "\\") o++, n--;
    if (o % 2 === 0) return r;
  }
  return -1;
}
function Gte(e, t) {
  for (let r = e.length - 1; r >= 0; r--) if (e[r] === t) {
    let o = 0, n = r - 1;
    while (n >= 0 && e[n] === "\\") o++, n--;
    if (o % 2 === 0) return r;
  }
  return -1;
}
var pg = { filePatternTools: ["Read", "Write", "Edit", "Glob", "NotebookRead", "NotebookEdit", "Cd"], bashPrefixTools: ["Bash"], customValidation: { WebSearch: (e) => {
  if (e.includes("*") || e.includes("?")) return { valid: false, error: "WebSearch does not support wildcards", suggestion: "Use exact search terms without * or ?", examples: ["WebSearch(claude ai)", "WebSearch(typescript tutorial)"] };
  return { valid: true };
}, WebFetch: (e) => {
  if (e.includes("://") || e.startsWith("http")) return { valid: false, error: "WebFetch permissions use domain format, not URLs", suggestion: 'Use "domain:hostname" format', examples: ["WebFetch(domain:example.com)", "WebFetch(domain:github.com)"] };
  if (!e.startsWith("domain:")) return { valid: false, error: 'WebFetch permissions must use "domain:" prefix', suggestion: 'Use "domain:hostname" format', examples: ["WebFetch(domain:example.com)", "WebFetch(domain:*.google.com)"] };
  return { valid: true };
} } };
function oU(e) {
  return pg.filePatternTools.includes(e);
}
function iU(e) {
  return pg.bashPrefixTools.includes(e);
}
function sU(e) {
  return Object.hasOwn(pg.customValidation, e) ? pg.customValidation[e] : void 0;
}
function cU(e, t) {
  let r = 0, o = t - 1;
  while (o >= 0 && e[o] === "\\") r++, o--;
  return r % 2 !== 0;
}
function Zx(e, t) {
  let r = 0;
  for (let o = 0; o < e.length; o++) if (e[o] === t && !cU(e, o)) r++;
  return r;
}
function Jte(e) {
  for (let t = 0; t < e.length - 1; t++) if (e[t] === "(" && e[t + 1] === ")") {
    if (!cU(e, t)) return true;
  }
  return false;
}
function aU(e) {
  if (!qx(e)) return null;
  let t = Hx(e);
  if (t && !qx(t.serverName)) return null;
  return { valid: false, error: `Wildcard tool name "${e}" is not supported in allow rules`, suggestion: "An allow pattern must name the scope it widens \u2014 globs are permitted only in the tool position after a literal mcp__<server>__ prefix. Deny and ask rules accept wildcards anywhere", examples: ["mcp__puppeteer__*", "mcp__github__get_*"] };
}
function Vx(e, t) {
  if (!e || e.trim() === "") return { valid: false, error: "Permission rule cannot be empty" };
  let r = Zx(e, "("), o = Zx(e, ")");
  if (r !== o) return { valid: false, error: "Mismatched parentheses", suggestion: "Ensure all opening parentheses have matching closing parentheses" };
  if (Jte(e)) {
    let a = e.substring(0, e.indexOf("("));
    if (!a) return { valid: false, error: "Empty parentheses with no tool name", suggestion: "Specify a tool name before the parentheses" };
    return { valid: false, error: "Empty parentheses", suggestion: `Either specify a pattern or use just "${a}" without parentheses`, examples: [`${a}`, `${a}(some-pattern)`] };
  }
  let n = nU(e), i = Hx(n.toolName);
  if (i) {
    if (n.ruleContent !== void 0 || Zx(e, "(") > 0) return { valid: false, error: "MCP rules do not support patterns in parentheses", suggestion: `Use "${n.toolName}" without parentheses, or use "mcp__${i.serverName}__*" for all tools`, examples: [`mcp__${i.serverName}`, `mcp__${i.serverName}__*`, i.toolName && i.toolName !== "*" ? `mcp__${i.serverName}__${i.toolName}` : void 0].filter(Boolean) };
    if (t === "allow") {
      let a = aU(n.toolName);
      if (a) return a;
    }
    return { valid: true };
  }
  if (!n.toolName || n.toolName.length === 0) return { valid: false, error: "Tool name cannot be empty" };
  if (t === "allow") {
    let a = aU(n.toolName);
    if (a) return a;
  }
  if (!n.toolName.includes("_") && n.toolName[0] !== n.toolName[0]?.toUpperCase()) return { valid: false, error: "Tool names must start with uppercase", suggestion: `Use "${Eh(String(n.toolName))}"` };
  let s = sU(n.toolName);
  if (s && n.ruleContent !== void 0) {
    let a = s(n.ruleContent);
    if (!a.valid) return a;
  }
  if (iU(n.toolName) && n.ruleContent !== void 0) {
    let a = n.ruleContent;
    if (a.includes(":*") && !a.endsWith(":*")) return { valid: false, error: "The :* pattern must be at the end", suggestion: "Move :* to the end for prefix matching, or use * for wildcard matching", examples: ["Bash(npm run:*) - prefix matching (legacy)", "Bash(npm run *) - wildcard matching"] };
    if (a === ":*") return { valid: false, error: "Prefix cannot be empty before :*", suggestion: "Specify a command prefix before :*", examples: ["Bash(npm *)", "Bash(git *)"] };
  }
  if (oU(n.toolName) && n.ruleContent !== void 0) {
    if (n.ruleContent.includes(":*")) return { valid: false, error: 'The ":*" syntax is only for Bash prefix rules', suggestion: 'Use glob patterns like "*" or "**" for file matching', examples: [`${n.toolName}(*.ts) - matches .ts files`, `${n.toolName}(src/**) - matches all files in src`, `${n.toolName}(**/*.test.ts) - matches test files`] };
  }
  return { valid: true };
}
var Wx = P(() => uU());
var lU = P(() => uU("allow"));
function uU(e) {
  return l.string().superRefine((t, r) => {
    let o = Vx(t, e);
    if (!o.valid) {
      let n = o.error;
      if (o.suggestion) n += `. ${o.suggestion}`;
      if (o.examples && o.examples.length > 0) n += `. Examples: ${o.examples.join(", ")}`;
      r.addIssue({ code: l.ZodIssueCode.custom, message: n, params: { received: t } });
    }
  });
}
var Xte = P(() => l.record(l.string(), l.coerce.string()));
function gU(e) {
  return l.object({ allow: l.array(lU()).optional().describe("List of permission rules for allowed operations"), deny: l.array(Wx()).optional().describe("List of permission rules for denied operations"), ask: l.array(Wx()).optional().describe("List of permission rules that should always prompt for confirmation"), defaultMode: l.enum([...iu, ...eU(e)]).optional().describe("Default permission mode when Claude Code needs access"), disableBypassPermissionsMode: l.enum(["disable"]).optional().describe("Disable the ability to bypass permission prompts"), ...Qj(e), additionalDirectories: l.array(l.string()).optional().describe("Additional directories to include in the permission scope") }).passthrough();
}
var NOe = P(() => gU(Bx()));
var Yte = P(() => l.object({ source: au().describe("Where to fetch the marketplace from"), installLocation: l.string().optional().describe("Local cache path where marketplace manifest is stored (auto-generated if not provided)"), autoUpdate: l.boolean().optional().describe("Whether to automatically update this marketplace and its installed plugins on startup") }));
var fg = P(() => l.object({ serverName: l.string().regex(/^[a-zA-Z0-9_-]+$/, "Server name can only contain letters, numbers, hyphens, and underscores").optional().describe("Name of the MCP server that users are allowed to configure"), serverCommand: l.array(l.string()).min(1, "Server command must have at least one element (the command)").optional().describe("Command array [command, ...args] to match exactly for allowed stdio servers"), serverUrl: l.string().optional().describe('URL pattern with wildcard support (e.g., "https://*.example.com/*") for allowed remote MCP servers') }).refine((e) => Zy([e.serverName !== void 0, e.serverCommand !== void 0, e.serverUrl !== void 0], Boolean) === 1, { message: 'Entry must have exactly one of "serverName", "serverCommand", or "serverUrl"' }));
var mg = P(() => l.object({ serverName: l.string().regex(/^[a-zA-Z0-9_-]+$/, "Server name can only contain letters, numbers, hyphens, and underscores").optional().describe("Name of the MCP server that is explicitly blocked"), serverCommand: l.array(l.string()).min(1, "Server command must have at least one element (the command)").optional().describe("Command array [command, ...args] to match exactly for blocked stdio servers"), serverUrl: l.string().optional().describe('URL pattern with wildcard support (e.g., "https://*.example.com/*") for blocked remote MCP servers') }).refine((e) => Zy([e.serverName !== void 0, e.serverCommand !== void 0, e.serverUrl !== void 0], Boolean) === 1, { message: 'Entry must have exactly one of "serverName", "serverCommand", or "serverUrl"' }));
var Qte = P(() => l.object({ path: l.string().describe("Absolute path to the helper executable"), timeoutMs: l.number().int().min(1e3).optional(), refreshIntervalMs: l.union([l.literal(0), l.number().int().min(6e4)]).optional() }));
var dU = ["skills", "agents", "hooks", "mcp"];
var pU = Object.freeze({ type: "invalid-entry-stripped" });
var ere = P(() => l.union([l.object({ type: l.literal("regex").describe('Config variant. This client understands "regex": matches turn output and builds a URL from named capture groups. Entries with other variants are preserved but skipped at runtime.'), pattern: l.string().describe("Regex matched against turn output (tool results and assistant text)"), url: l.string().describe("Link target. {name} placeholders are filled from named regex capture groups, e.g. (?<id>...) -> {id}. Values are URL-encoded; the origin must be literal in the template. The scheme must be https, http, or a recognized editor or workspace deep-link scheme: vscode, vscode-insiders, cursor, windsurf, zed, jetbrains, idea, slack, linear, notion, figma."), label: l.string().optional().describe("Badge text. {name} placeholders filled from named capture groups; defaults to the full match.") }).passthrough(), l.object({ type: l.string().describe("Config variant discriminator for entries this client does not understand; the entry is preserved as-is and skipped at runtime.") }).passthrough()]));
function hU(e) {
  return l.object({ $schema: l.string().optional().describe("JSON Schema reference for Claude Code settings"), apiKeyHelper: l.string().optional().describe("Path to a script that outputs authentication values"), proxyAuthHelper: l.string().optional().describe("Shell command that outputs a Proxy-Authorization header value (EAP)"), awsCredentialExport: l.string().optional().describe("Path to a script that exports AWS credentials"), awsAuthRefresh: l.string().optional().describe("Path to a script that refreshes AWS authentication"), gcpAuthRefresh: l.string().optional().describe("Command to refresh GCP authentication (e.g., gcloud auth application-default login)"), policyHelper: Qte().optional().describe("Executable that computes managed settings at startup. Honored only from admin-controlled policy sources."), ...Ee(process.env.CLAUDE_CODE_ENABLE_XAA) && { xaaIdp: l.object({ issuer: l.string().url().describe("IdP issuer URL for OIDC discovery"), clientId: l.string().describe("Claude Code's client_id registered at the IdP"), callbackPort: l.number().int().positive().optional().describe("Fixed loopback callback port for the IdP OIDC login. Only needed if the IdP does not honor RFC 8252 port-any matching.") }).optional().describe("XAA (SEP-990) IdP connection. Configure once; all XAA-enabled MCP servers reuse this.") }, fileSuggestion: l.object({ type: l.literal("command"), command: l.string() }).optional().describe("Custom file suggestion configuration for @ mentions"), respectGitignore: l.boolean().optional().describe("Whether file picker should respect .gitignore files (default: true). Note: .ignore files are always respected."), breakReminder: l.object({ enabled: l.boolean().optional().describe("Show a friendly nudge after sustained continuous use (default false). Must be true for the reminder to fire."), intervalMinutes: l.number().int().positive().optional().describe("Minutes of continuous use before the reminder fires (default 120). Re-fires every interval until you take a break."), breakThresholdMinutes: l.number().int().positive().optional().describe("Minutes of inactivity that count as a break and reset the timer (default 15)"), message: l.string().optional().describe("Custom reminder text. Leave unset for a rotating set of friendly nudges.") }).optional().describe("@internal Opt-in break reminder. When enabled, shows a dismissible nudge after sustained continuous use. Never blocks \u2014 just a friendly heads-up."), quietHours: l.object({ enabled: l.boolean().optional().describe("Show a one-time nudge when you start or keep using the CLI inside your quiet-hours window (default false)."), start: l.string().regex(/^([01]?\d|2[0-3]):[0-5]\d$/, 'Expected 24-hour local time "HH:MM" (e.g. "22:00")').optional().describe('Start of the quiet-hours window, 24-hour local time "HH:MM".'), end: l.string().regex(/^([01]?\d|2[0-3]):[0-5]\d$/, 'Expected 24-hour local time "HH:MM" (e.g. "07:00")').optional().describe('End of the quiet-hours window, 24-hour local time "HH:MM". May be earlier than start for an overnight range.') }).optional().describe("@internal Opt-in quiet hours. When enabled, shows a single soft nudge per session while inside the configured local-time window. Never blocks."), cleanupPeriodDays: l.number().int().positive().optional().describe("Number of days to retain chat transcripts before automatic cleanup (default: 30). Minimum 1. Use a large value for long retention; use --no-session-persistence to disable transcript writes entirely."), skillListingMaxDescChars: l.number().int().positive().optional().describe("Per-skill description character cap in the skill listing sent to Claude (default: 1536). Descriptions longer than this are truncated. Raise to opt in to higher per-turn context cost."), skillListingBudgetFraction: l.number().gt(0).lte(1).optional().describe("Fraction of the context window (in characters) reserved for the skill listing sent to Claude (default: 0.01 = 1%). When the listing exceeds this, descriptions are shortened to fit. Raise to opt in to higher per-turn context cost."), wslInheritsWindowsSettings: l.boolean().optional().describe("When set to true in either admin-only Windows source \u2014 the HKLM SOFTWARE/Policies/ClaudeCode registry key or C:/Program Files/ClaudeCode/managed-settings.json \u2014 WSL reads managed settings from the full Windows policy chain (HKLM, C:/Program Files/ClaudeCode via DrvFs, HKCU) in addition to /etc/claude-code. Windows sources take priority. The flag is also required in HKCU itself for HKCU policy to apply on WSL (double opt-in: admin enables the chain, user confirms HKCU). On native Windows the flag has no effect."), env: Xte().optional().describe("Environment variables to set for Claude Code sessions"), attribution: l.object({ commit: l.string().optional().describe("Attribution text for git commits, including any trailers. Empty string hides attribution."), pr: l.string().optional().describe("Attribution text for pull request descriptions. Empty string hides attribution.") }).optional().describe("Customize attribution text for commits and PRs. Each field defaults to the standard Claude Code attribution if not set."), includeCoAuthoredBy: l.boolean().optional().describe("Deprecated: Use attribution instead. Whether to include Claude's co-authored by attribution in commits and PRs (defaults to true)"), ...false, includeGitInstructions: l.boolean().optional().describe("Include built-in commit and PR workflow instructions in Claude's system prompt (default: true)"), permissions: gU(e).optional().describe("Tool usage permissions configuration"), model: l.string().optional().describe("Override the default model used by Claude Code"), fallbackModel: l.array(l.string()).optional().describe('Fallback model(s) tried in order when the primary model is overloaded or unavailable. Each element accepts a model name or alias; "default" expands to the default model. CLI --fallback-model takes precedence.'), availableModels: l.array(l.string()).optional().describe('Allowlist of models that users can select. Accepts family aliases ("opus" allows any opus version), version prefixes ("opus-4-5" allows only that version), and full model IDs. If undefined, all models are available. If empty array, only the default model is available. Typically set in managed settings by enterprise administrators.'), enforceAvailableModels: l.boolean().optional().describe("When true and availableModels is a non-empty array, the Default model selection is also constrained: if the default model for the user tier is not in availableModels, Default resolves to the first allowed availableModels entry instead. Has no effect when availableModels is unset or an empty array. Typically set in managed settings by enterprise administrators."), modelOverrides: l.record(l.string(), l.string()).optional().describe('Override mapping from Anthropic model ID (e.g. "claude-opus-4-6") to provider-specific model ID (e.g. a Bedrock inference profile ARN). Typically set in managed settings by enterprise administrators.'), enableAllProjectMcpServers: l.boolean().optional().describe("Whether to automatically approve all MCP servers in the project"), enabledMcpjsonServers: l.array(l.string()).optional().describe("List of approved MCP servers from .mcp.json"), disabledMcpjsonServers: l.array(l.string()).optional().describe("List of rejected MCP servers from .mcp.json"), skillOverrides: l.record(l.string(), l.enum(["on", "name-only", "user-invocable-only", "off"])).optional().describe('Per-skill listing overrides keyed by skill name. "name-only" lists the skill without its description; "user-invocable-only" hides it from the model but keeps /name; "off" hides it from both. Absent = on.'), disableBundledSkills: l.boolean().optional().describe("Disable the skills and workflows that ship with Claude Code: bundled skills and workflows are removed entirely; built-in slash commands stay typable but are hidden from the model. Plugins, .claude/skills/, and .claude/commands/ are unaffected. Equivalent to CLAUDE_CODE_DISABLE_BUNDLED_SKILLS=1."), allowedMcpServers: l.array(fg()).optional().describe("Enterprise allowlist of MCP servers that can be used. Applies to all scopes including enterprise servers from managed-mcp.json. If undefined, all servers are allowed. If empty array, no servers are allowed. Denylist takes precedence - if a server is on both lists, it is denied."), deniedMcpServers: l.array(mg()).optional().describe("Enterprise denylist of MCP servers that are explicitly blocked. If a server is on the denylist, it will be blocked across all scopes including enterprise. Denylist takes precedence over allowlist - if a server is on both lists, it is denied."), hooks: Zo().optional().describe("Custom commands to run before/after tool executions"), worktree: l.object({ symlinkDirectories: l.array(l.string()).optional().describe('Directories to symlink from main repository to worktrees to avoid disk bloat. Must be explicitly configured - no directories are symlinked by default. Common examples: "node_modules", ".cache", ".bin"'), sparsePaths: l.array(l.string()).optional().describe("Directories to include when creating worktrees, via git sparse-checkout (cone mode). Dramatically faster in large monorepos \u2014 only the listed paths are written to disk."), baseRef: l.enum(["fresh", "head"]).optional().describe("Which ref new worktrees branch from. 'fresh' (default) branches from origin/<default-branch> for a clean tree. 'head' branches from your current local HEAD so unpushed commits and feature-branch state are present. Applies to --worktree, EnterWorktree, and agent isolation."), bgIsolation: l.enum(["worktree", "none"]).optional().catch(void 0).describe("Isolation mode for background sessions in this repo. 'worktree' (default) blocks Edit/Write in the main checkout until EnterWorktree is called. 'none' lets background jobs edit the working copy directly.") }).optional().describe("Git worktree configuration for --worktree flag."), disableAllHooks: l.boolean().optional().describe("Disable all hooks and statusLine execution"), disableAgentView: l.boolean().optional().describe("Disable agent view (`claude agents`, `--bg`, /background, the on-demand daemon). Typically set in managed settings. Equivalent to CLAUDE_CODE_DISABLE_AGENT_VIEW=1."), disableRemoteControl: l.boolean().optional().describe("Disable Remote Control (claude.ai/code, `claude remote-control`, `--remote-control`/`--rc`, auto-start, and the in-session toggle). Typically set in managed settings."), disableWorkflows: l.boolean().optional().describe("Disable the Workflows feature (also via CLAUDE_CODE_DISABLE_WORKFLOWS)."), disableArtifact: l.boolean().optional().describe("Disable the Artifact tool (also via CLAUDE_CODE_DISABLE_ARTIFACT)."), enableWorkflows: l.boolean().optional().describe("Enable or disable the Workflows feature for this user. Unset = default by plan once the feature is available."), workflowKeywordTriggerEnabled: l.boolean().optional().describe('Enable the "ultracode" keyword trigger: including the keyword in a prompt opts that turn into the Workflow tool. Set to false to disable the trigger. Default: true.'), disableSkillShellExecution: l.boolean().optional().describe("Disable inline shell execution in skills and custom slash commands from user, project, or plugin sources. Commands are replaced with a placeholder instead of being run."), defaultShell: l.enum(["bash", "powershell"]).optional().describe("Default shell for input-box ! commands. Defaults to 'bash' on all platforms (no Windows auto-flip)."), allowManagedHooksOnly: l.boolean().optional().describe("When true (and set in managed settings), only hooks from managed settings run. User, project, and local hooks are ignored."), allowedHttpHookUrls: l.array(l.string()).optional().describe('Allowlist of URL patterns that HTTP hooks may target. Supports * as a wildcard (e.g. "https://hooks.example.com/*"). When set, HTTP hooks with non-matching URLs are blocked. If undefined, all URLs are allowed. If empty array, no HTTP hooks are allowed. Arrays merge across settings sources (same semantics as allowedMcpServers).'), httpHookAllowedEnvVars: l.array(l.string()).optional().describe("Allowlist of environment variable names HTTP hooks may interpolate into headers. When set, each hook's effective allowedEnvVars is the intersection with this list. If undefined, no restriction is applied. Arrays merge across settings sources (same semantics as allowedMcpServers)."), allowManagedPermissionRulesOnly: l.boolean().optional().describe("When true (and set in managed settings), only permission rules (allow/deny/ask) from managed settings are respected. User, project, local, and CLI argument permission rules are ignored."), allowManagedMcpServersOnly: l.boolean().optional().describe("When true (and set in managed settings), allowedMcpServers is only read from managed settings. deniedMcpServers still merges from all sources, so users can deny servers for themselves. Users can still add their own MCP servers, but only the admin-defined allowlist applies."), allowAllClaudeAiMcps: l.boolean().optional().describe("When true (and set in managed settings), claude.ai cloud MCP connectors load alongside managed-mcp.json instead of being suppressed by its exclusive-control lockdown. Default off preserves the lockdown. Read from managed settings only."), strictPluginOnlyCustomization: l.preprocess((t) => Array.isArray(t) ? t.filter((r) => dU.includes(r)) : t, l.union([l.boolean(), l.array(l.enum(dU))])).optional().catch(void 0).describe('When set in managed settings, blocks non-plugin customization sources for the listed surfaces. Array form locks specific surfaces (e.g. ["skills", "hooks"]); `true` locks all four; `false` is an explicit no-op. Blocked: ~/.claude/{surface}/, .claude/{surface}/ (project), settings.json hooks, .mcp.json. NOT blocked: managed (policySettings) sources, plugin-provided customizations. Composes with strictKnownMarketplaces for end-to-end admin control \u2014 plugins gated by marketplace allowlist, everything else blocked here.'), statusLine: l.object({ type: l.literal("command"), command: l.string(), padding: l.number().optional(), refreshInterval: l.number().min(1).optional().catch(void 0).describe("Re-run the status line command every N seconds in addition to event-driven updates"), hideVimModeIndicator: l.boolean().optional().describe("Hide the built-in `-- INSERT --` / `-- VISUAL --` indicator below the prompt. Use this when your status line script renders `vim.mode` itself.") }).optional().describe("Custom status line display configuration"), prUrlTemplate: l.string().optional().describe('URL template for PR links in the footer link badges and inline messages. The detected git PR is rendered as the first footer-link badge. Placeholders: {host} {owner} {repo} {number} {url}. Example: "https://reviews.example.com/{owner}/{repo}/pull/{number}"'), footerLinksRegexes: l.array(ere().catch(pU)).transform((t) => t.filter((r) => r !== pU)).optional().catch(void 0).describe("Extra clickable footer badges that appear when a regex matches turn output (tool results and assistant responses). Read from user, flag, and managed settings only; ignored in project .claude/settings.json and local .claude/settings.local.json. At most 5 badges render; the oldest is displaced by newer matches and /clear removes them. Use to surface IDs printed by project CLIs as session links."), subagentStatusLine: l.object({ type: l.literal("command"), command: l.string() }).optional().describe("Custom per-subagent status line shown in the agent panel; receives row context as JSON on stdin"), enabledPlugins: l.record(l.string(), l.union([l.array(l.string()), l.boolean(), l.undefined()])).optional().describe('Enabled plugins using plugin-id@marketplace-id format. Example: { "formatter@anthropic-tools": true }. Also supports extended format with version constraints. Settings precedence is user < project < local < flag < policy, so to disable a plugin that project settings enable, set it to false in .claude/settings.local.json \u2014 setting false in ~/.claude/settings.json is overridden by the project.'), extraKnownMarketplaces: l.record(l.string(), Yte()).check((t) => {
    for (let [r, o] of Object.entries(t.value)) if (o.source.source === "settings" && o.source.name !== r) t.issues.push({ code: "custom", input: o.source.name, path: [r, "source", "name"], message: `Settings-sourced marketplace name must match its extraKnownMarketplaces key (got key "${r}" but source.name "${o.source.name}")` });
  }).optional().describe("Additional marketplaces to make available for this repository. Typically used in repository .claude/settings.json to ensure team members have required plugin sources."), strictKnownMarketplaces: l.array(au()).optional().describe("Enterprise strict list of allowed marketplace sources. When set in managed settings, ONLY these exact sources can be added as marketplaces. The check happens BEFORE downloading, so blocked sources never touch the filesystem. Note: this is a policy gate only \u2014 it does NOT register marketplaces. To pre-register allowed marketplaces for users, also set extraKnownMarketplaces."), blockedMarketplaces: l.array(au()).optional().describe("Enterprise blocklist of marketplace sources. When set in managed settings, these exact sources are blocked from being added as marketplaces. The check happens BEFORE downloading, so blocked sources never touch the filesystem."), pluginSuggestionMarketplaces: l.array(l.string()).optional().describe("Marketplace names whose plugins may surface as contextual install suggestions (relevance-based tips). No marketplace-declared suggestions surface without this allowlist; the built-in first-party frontend-design tip is unaffected. Only honored when set in managed settings (policy scope); the key is ignored in user, project, and local settings. A name only takes effect when the marketplace is registered on the machine AND its registered source is also declared in managed settings, either as the extraKnownMarketplaces entry for that name or as an entry of strictKnownMarketplaces. A marketplace registered from a different source under an allowlisted name is ignored. The official marketplace is exempt from the source requirement: allowlisting its name alone suffices, since that name can only register from the official Anthropic source."), forceLoginMethod: l.enum(["claudeai", "console", "gateway"]).optional().catch(void 0).describe('Force a specific login method: "claudeai" for Claude Pro/Max, "console" for Console billing, "gateway" for the Cloud gateway OIDC device flow'), forceLoginGatewayUrl: l.string().url().optional().catch(void 0).describe('@internal Cloud gateway URL to pre-fill and auto-connect to during login. Typically set in local managed settings alongside forceLoginMethod: "gateway" so users never type the URL. Hidden from public SDK types until Cloud gateway is documented.'), parentSettingsBehavior: l.enum(["first-wins", "merge"]).optional().describe(`Controls whether the SDK parent tier (Options.managedSettings / --managed-settings) layers under this admin tier. "first-wins" (default): parent is dropped \u2014 admin tiers are the only policy source. "merge": parent's restrictive-only-filtered settings union under the admin winner. Has no effect when no admin tier exists (parent applies as the sole policy tier, still filtered restrictive-only).`), forceLoginOrgUUID: l.union([l.string(), l.array(l.string())]).optional().describe("Organization UUID to require for OAuth login. Accepts a single UUID string or an array of UUIDs (any one is permitted). When set in managed settings, login fails if the authenticated account does not belong to a listed organization."), forceRemoteSettingsRefresh: l.boolean().optional().describe("When set in managed settings, the CLI blocks startup until remote managed settings are freshly fetched, and exits if the fetch fails"), otelHeadersHelper: l.string().optional().describe("Path to a script that outputs OpenTelemetry headers"), outputStyle: l.string().optional().describe("Controls the output style for assistant responses"), viewMode: l.enum(["default", "verbose", "focus"]).optional().catch(void 0).describe("Default transcript view mode on startup"), language: l.string().optional().describe('Preferred language for Claude responses and voice dictation (e.g., "japanese", "spanish")'), skipWebFetchPreflight: l.boolean().optional().describe("Skip the WebFetch blocklist check for enterprise environments with restrictive security policies"), sandbox: $j().optional(), feedbackSurveyRate: l.number().min(0).max(1).optional().describe("Probability (0\u20131) that the session quality survey appears when eligible. 0.05 is a reasonable starting point."), spinnerTipsEnabled: l.boolean().optional().describe("Whether to show tips in the spinner"), spinnerVerbs: l.object({ mode: l.enum(["append", "replace"]), verbs: l.array(l.string()) }).optional().describe('Customize spinner verbs. mode: "append" adds verbs to defaults, "replace" uses only your verbs.'), spinnerTipsOverride: l.object({ excludeDefault: l.boolean().optional(), tips: l.array(l.string()) }).optional().describe("Override spinner tips. tips: array of tip strings. excludeDefault: if true, only show custom tips (default: false)."), syntaxHighlightingDisabled: l.boolean().optional().describe("Whether to disable syntax highlighting in diffs"), terminalTitleFromRename: l.boolean().optional().describe("Whether /rename updates the terminal tab title (defaults to true). Set to false to keep auto-generated topic titles."), alwaysThinkingEnabled: l.boolean().optional().describe("When false, thinking is disabled. When absent or true, thinking is enabled automatically for supported models."), effortLevel: l.enum(["low", "medium", "high", "xhigh"]).optional().catch(void 0).describe("Persisted effort level for supported models."), ultracode: l.boolean().optional().catch(void 0).describe("Enable ultracode for the session: xhigh effort plus standing dynamic-workflow orchestration. Session-scoped \u2014 typically provided via --settings or the apply_flag_settings control request; interactive toggles never persist it. Requires workflows to be enabled and an xhigh-capable model."), autoCompactWindow: l.number().int().min(1e5).max(1e6).optional().catch(void 0).describe("Auto-compact window size"), advisorModel: l.string().optional().describe("Advisor model for the server-side advisor tool."), fastMode: l.boolean().optional().describe("When true, fast mode is enabled. When absent or false, fast mode is off."), fastModePerSessionOptIn: l.boolean().optional().describe("When true, fast mode does not persist across sessions. Each session starts with fast mode off."), promptSuggestionEnabled: l.boolean().optional().describe("When false, prompt suggestions are disabled. When absent or true, prompt suggestions are enabled."), awaySummaryEnabled: l.boolean().optional().describe("@internal When false, the session recap (shown when you return after being away for 5+ minutes) is disabled. When absent or true, recap is enabled. Hidden from public SDK types until external launch."), showClearContextOnPlanAccept: l.boolean().optional().describe('When true, the plan-approval dialog offers a "clear context" option. Defaults to false.'), agent: l.string().optional().describe("Name of an agent (built-in or custom) to use for the main thread. Applies the agent's system prompt, tool restrictions, and model."), companyAnnouncements: l.array(l.string()).optional().describe("Company announcements to display at startup (one will be randomly selected if multiple are provided)"), pluginConfigs: l.record(l.string(), l.object({ mcpServers: l.record(l.string(), l.record(l.string(), l.union([l.string(), l.number(), l.boolean(), l.array(l.string())]))).optional().describe("User configuration values for MCP servers keyed by server name"), options: l.record(l.string(), l.union([l.string(), l.number(), l.boolean(), l.array(l.string())])).optional().describe("Non-sensitive option values from plugin manifest userConfig, keyed by option name. Sensitive values go to secure storage instead.") })).optional().describe("Per-plugin configuration including MCP server user configs, keyed by plugin ID (plugin@marketplace format)"), remote: l.object({ defaultEnvironmentId: l.string().optional().describe("Default environment ID to use for cloud sessions") }).optional().describe("Cloud session configuration"), autoUpdatesChannel: l.enum(["latest", "stable", "rc"]).optional().describe("Release channel for auto-updates (latest or stable)"), minimumVersion: l.string().optional().describe("Minimum version to stay on - prevents downgrades when switching to stable channel"), requiredMinimumVersion: l.string().optional().describe("Minimum Claude Code version required to start. If the running version is older, Claude Code exits at startup with instructions to update. Only enforced from managed (policy) settings."), requiredMaximumVersion: l.string().optional().describe("Maximum Claude Code version allowed to start. If the running version is newer, Claude Code exits at startup with instructions to install an approved version. Only enforced from managed (policy) settings."), plansDirectory: l.string().optional().describe("Custom directory for plan files, relative to project root. If not set, defaults to ~/.claude/plans/"), tui: l.enum(["default", "fullscreen"]).optional().describe('Terminal UI renderer. "fullscreen" uses the flicker-free alt-screen renderer with virtualized scrollback (equivalent to CLAUDE_CODE_NO_FLICKER=1). "default" uses the classic main-screen renderer.'), ...false, voice: l.object({ enabled: l.boolean().optional(), mode: l.enum(["hold", "tap"]).optional().describe("'hold' (default): hold to talk. 'tap': tap to start, tap to stop+submit."), autoSubmit: l.boolean().optional().describe("Submit the prompt when hold-to-talk is released (hold mode only)") }).optional().describe("Voice mode settings (hold-to-talk / tap-to-toggle dictation)"), channelsEnabled: l.boolean().optional().describe("Managed-org opt-in for channel notifications (MCP servers with the claude/channel capability pushing inbound messages). claude.ai Teams/Enterprise: default off. Console: default on unless managed settings exist. Set true to allow; users then select servers via --channels."), allowedChannelPlugins: l.array(l.object({ marketplace: l.string(), plugin: l.string() })).optional().describe("Managed-org allowlist of channel plugins. When set, replaces the default Anthropic allowlist \u2014 admins decide which plugins may push inbound messages. Undefined falls back to the default. Requires channelsEnabled: true."), prefersReducedMotion: l.boolean().optional().describe("Reduce or disable animations for accessibility (spinner shimmer, flash effects, etc.)"), doneMeansMerged: l.boolean().optional().describe("@internal When true, Claude keeps working until the PR is ready for you to merge, a cron/Monitor is armed to resume later, or it hands you a self-contained next step."), totalTokensReminder: l.enum(["off", "infinite", "fixed", "countdown"]).optional().describe("@internal Emit a <total_tokens>N tokens left</total_tokens> block in the system prompt and after each tool result. 'infinite' uses the literal value Infinite, 'fixed' uses 5000000, 'countdown' uses the live remaining context-window tokens. Defaults to off. Env var CLAUDE_CODE_TOTAL_TOKENS_REMINDER overrides."), autoMemoryEnabled: l.boolean().optional().describe("Enable auto-memory for this project. When false, Claude will not read from or write to the auto-memory directory."), autoMemoryDirectory: l.string().optional().describe("Custom directory path for auto-memory storage. Supports ~/ prefix for home directory expansion. Ignored if set in projectSettings (checked-in .claude/settings.json) for security. When unset, defaults to ~/.claude/projects/<sanitized-cwd>/memory/."), autoDreamEnabled: l.boolean().optional().describe("Enable background memory consolidation (auto-dream). When set, overrides the server-side default."), showThinkingSummaries: l.boolean().optional().describe("Request API-side thinking summaries and show them in the conversation and in the transcript view (ctrl+o). Set explicitly to override the default for your install."), skipDangerousModePermissionPrompt: l.boolean().optional().describe("Whether the user has accepted the bypass permissions mode dialog"), skipWorkflowUsageWarning: l.boolean().optional().describe("@internal Whether the user has accepted the multi-agent workflow usage warning. Until set, auto permission mode prompts before running a workflow."), disableAutoMode: l.enum(["disable"]).optional().describe("Disable auto mode"), sshConfigs: l.array(l.object({ id: l.string().describe("Unique identifier for this SSH config. Used to match configs across settings sources."), name: l.string().describe("Display name for the SSH connection"), sshHost: l.string().describe('SSH host in format "user@hostname" or "hostname", or a host alias from ~/.ssh/config'), sshPort: l.number().int().optional().describe("SSH port (default: 22)"), sshIdentityFile: l.string().optional().describe("Path to SSH identity file (private key)"), startDirectory: l.string().optional().describe("Default working directory on the remote host. Supports tilde expansion (e.g. ~/projects). If not specified, defaults to the remote user home directory. Can be overridden by the [dir] positional argument in `claude ssh <config> [dir]`.") })).optional().describe("SSH connection configurations for remote environments. Typically set in managed settings by enterprise administrators to pre-configure SSH connections for team members."), claudeMd: l.string().optional().describe("CLAUDE.md-style instructions injected as organization-managed memory. Only honored from managed/policy settings."), claudeMdExcludes: l.array(l.string()).optional().describe('Glob patterns or absolute paths of CLAUDE.md files to exclude from loading. Patterns are matched against absolute file paths using picomatch. Only applies to User, Project, and Local memory types (Managed/policy files cannot be excluded). Examples: "/home/user/monorepo/CLAUDE.md", "**/code/CLAUDE.md", "**/some-dir/.claude/rules/**"'), pluginTrustMessage: l.string().optional().describe('Custom message to append to the plugin trust warning shown before installation. Only read from policy settings (managed-settings.json / MDM). Useful for enterprise administrators to add organization-specific context (e.g., "All plugins from our internal marketplace are vetted and approved.").'), theme: l.union([l.enum(Mj), l.string().startsWith("custom:").transform((t) => t)]).optional().catch(void 0).describe("Color theme for the UI"), editorMode: l.enum(Aj).optional().catch(void 0).describe("Key binding mode for the prompt input"), verbose: l.boolean().optional().describe("Show full tool output instead of truncated summaries"), preferredNotifChannel: l.enum(Oj).optional().catch(void 0).describe("Preferred OS notification channel"), autoCompactEnabled: l.boolean().optional().describe("Automatically compact conversation when context fills"), precomputeCompactionEnabled: l.boolean().optional().describe("@internal Precompute the compaction summary in the background before it is needed. Only applies when auto-compact is on."), switchModelsOnFlag: l.boolean().optional().describe("When safety measures flag a message, automatically switch to a different model to keep chatting. When off, your session will pause instead."), autoScrollEnabled: l.boolean().optional().describe("Auto-scroll the conversation view to bottom (fullscreen mode only)"), wheelScrollAccelerationEnabled: l.boolean().optional().describe("Ramp mouse-wheel scroll speed during fast scrolls (fullscreen mode only)"), fileCheckpointingEnabled: l.boolean().optional().describe("Snapshot files before edits so /rewind can restore them"), showTurnDuration: l.boolean().optional().describe('Show "Cooked for Nm Ns" after each assistant turn'), showMessageTimestamps: l.boolean().optional().describe("Stamp each assistant message with its arrival time"), terminalProgressBarEnabled: l.boolean().optional().describe("Emit OSC 9;4 progress sequences during long operations"), todoFeatureEnabled: l.boolean().optional().describe("Enable the todo / task tracking panel"), teammateMode: l.enum(Cj).optional().catch(void 0).describe("How spawned teammates execute (tmux, in-process, auto)"), remoteControlAtStartup: l.boolean().optional().describe("Start Remote Control bridge automatically each session"), isolatePeerMachines: l.boolean().optional().describe("Require explicit approval before SendMessage can reach a peer session on another machine via Remote Control"), daemonColdStart: l.enum(["transient", "ask"]).optional().describe("When no background service is running: 'transient' spawns one for this login session; 'ask' offers to install it persistently"), autoUploadSessions: l.boolean().optional().describe("Mirror local sessions to claude.ai as view-only (no remote control)"), inputNeededNotifEnabled: l.boolean().optional().describe("Push to mobile when a permission prompt or question is waiting"), agentPushNotifEnabled: l.boolean().optional().describe("Allow Claude to push proactive mobile notifications"), ...Yj(e) }).passthrough();
}
var Wo = P(() => hU(Bx()));
var fU = Object.freeze({ serverName: "invalid-entry-stripped" });
var an = "https://code.claude.com/docs/en";
var tre = [{ matches: (e) => e.path === "permissions.defaultMode" && e.code === "invalid_value", tip: { suggestion: 'Valid modes: "acceptEdits" (ask before file changes), "plan" (analysis only), "bypassPermissions" (auto-accept all), or "default" (standard behavior)', docLink: `${an}/iam#permission-modes` } }, { matches: (e) => e.path === "apiKeyHelper" && e.code === "invalid_type", tip: { suggestion: 'Provide a shell command that outputs your API key to stdout. The script should output only the API key. Example: "/bin/generate_temp_api_key.sh"' } }, { matches: (e) => e.path === "cleanupPeriodDays" && e.code === "too_small", tip: { suggestion: 'cleanupPeriodDays must be at least 1. To keep transcripts for a long time, set a large number (e.g. 3650 for ~10 years). To disable transcript writes entirely, remove this setting and use the --no-session-persistence CLI flag or the SDK persistSession:false option instead. (0 is rejected because it previously silently disabled all transcript writes, which users setting it to mean "never clean up" did not expect.)' } }, { matches: (e) => e.path.startsWith("env.") && e.code === "invalid_type", tip: { suggestion: 'Environment variables must be strings. Wrap numbers and booleans in quotes. Example: "DEBUG": "true", "PORT": "3000"', docLink: `${an}/settings#environment-variables` } }, { matches: (e) => (e.path === "permissions.allow" || e.path === "permissions.deny") && e.code === "invalid_type" && e.expected === "array", tip: { suggestion: 'Permission rules must be in an array. Format: ["Tool(specifier)"]. Examples: ["Bash(npm run build)", "Edit(docs/**)", "Read(~/.zshrc)"]. Use * for wildcards.' } }, { matches: (e) => e.path.startsWith("hooks.") && e.code === "invalid_key", tip: { suggestion: "Not a recognized hook event. Common events: PreToolUse, PostToolUse, UserPromptSubmit, SessionStart, SessionEnd, Stop. Check spelling and capitalization.", docLink: `${an}/hooks` } }, { matches: (e) => /\.hooks\.\d+\.command$/.test(e.path) && e.code === "invalid_type" && e.received === "undefined", tip: { suggestion: 'Command hooks require `command`. For exec form (no shell), set `command` to the executable and `args` to its arguments: {"type": "command", "command": "echo", "args": ["hi"]}. For shell form, set `command` to the full shell string: {"type": "command", "command": "echo hi"}.', docLink: `${an}/hooks#exec-form-and-shell-form` } }, { matches: (e) => e.path.includes("hooks") && e.code === "invalid_type", tip: { suggestion: 'Hooks use a matcher + hooks array. The matcher is a string: a tool name ("Bash"), pipe-separated list ("Edit|Write"), or empty to match all. Example: {"PostToolUse": [{"matcher": "Edit|Write", "hooks": [{"type": "command", "command": "echo Done"}]}]}' } }, { matches: (e) => e.code === "invalid_type" && e.expected === "boolean", tip: { suggestion: 'Use true or false without quotes. Example: "includeCoAuthoredBy": true' } }, { matches: (e) => e.code === "unrecognized_keys", tip: { suggestion: "Check for typos or refer to the documentation for valid fields", docLink: `${an}/settings` } }, { matches: (e) => e.code === "invalid_value" && e.enumValues !== void 0, tip: { suggestion: void 0 } }, { matches: (e) => e.code === "invalid_type" && e.expected === "object" && e.received === null && e.path === "", tip: { suggestion: "Check for missing commas, unmatched brackets, or trailing commas. Use a JSON validator to identify the exact syntax error." } }, { matches: (e) => e.path === "permissions.additionalDirectories" && e.code === "invalid_type", tip: { suggestion: 'Must be an array of directory paths. Example: ["~/projects", "/tmp/workspace"]. You can also use --add-dir flag or /add-dir command', docLink: `${an}/iam#working-directories` } }];
var rre = { permissions: `${an}/iam#configuring-permissions`, env: `${an}/settings#environment-variables`, hooks: `${an}/hooks` };
var QOe = P(() => Wo().strict());
var ire = new Set(Xo);
var Yn = Object.freeze({ settings: {}, errors: [] });
process.env.NoDefaultCurrentDirectoryInExePath = "1";
async function Vre(e, t) {
  try {
    await zre(e, t);
  } catch (r) {
    if (!zr(r)) throw r;
  }
}
async function Wre(e, t) {
  if (!e) return;
  let r = e;
  try {
    let o = Ze(e);
    if (o?.claudeAiOauth?.refreshToken) delete o.claudeAiOauth.refreshToken, r = pe(o);
  } catch {
  }
  await QU(t, r, { mode: 384 });
}
function Kre() {
  if (process.platform !== "darwin") return Promise.resolve(void 0);
  let e = v0(_0);
  return new Promise((t) => {
    Nre("security", ["find-generic-password", "-a", S0(), "-w", "-s", e], { encoding: "utf-8", timeout: 5e3 }, (r, o) => t(r ? void 0 : o.trim() || void 0));
  });
}
async function rz(e, t, r, o, n = 6e4) {
  if (!Se(t)) return;
  let i = rr(r), s = await Ar(e.load({ projectKey: i, sessionId: t }), n, `SessionStore.load() timed out after ${n}ms for session ${t}`);
  if (!s || s.length === 0) return;
  let a = zt(Hre(), `claude-resume-${ow()}`);
  try {
    let c = zt(a, "projects", i);
    await Qx(c, { recursive: true });
    let u = zt(c, `${t}.jsonl`);
    await ec(u, s);
    let d = o?.CLAUDE_CONFIG_DIR ?? process.env.CLAUDE_CONFIG_DIR, p = d ?? zt(ew(), ".claude"), f;
    try {
      f = await YU(zt(p, ".credentials.json"), "utf-8");
    } catch (m) {
      if (!zr(m)) throw m;
    }
    if (!d && !(o ?? process.env).ANTHROPIC_API_KEY && !(o ?? process.env).CLAUDE_CODE_OAUTH_TOKEN) f = await Kre() ?? f;
    if (await Wre(f, zt(a, ".credentials.json")), await Vre(zt(d ?? ew(), ".claude.json"), zt(a, ".claude.json")), e.listSubkeys) {
      let m = zt(c, t), g = await Ar(e.listSubkeys({ projectKey: i, sessionId: t }), n, `SessionStore.listSubkeys() timed out after ${n}ms for session ${t}`);
      for (let h of g) {
        let y = fu(m, h + ".jsonl");
        if (!h || ez(h) || h.split(/[\\/]/).includes("..") || !y.startsWith(m + iw)) {
          ee(`[SessionStore] skipping unsafe subpath from listSubkeys: ${h}`, { level: "warn" });
          continue;
        }
        let v = await Ar(e.load({ projectKey: i, sessionId: t, subpath: h }), n, `SessionStore.load() timed out after ${n}ms for session ${t} subpath ${h}`);
        if (!v || v.length === 0) continue;
        let x = [], w = [];
        for (let A of v) if (nw(A)) x.push(A);
        else w.push(A);
        if (w.length > 0) await Qx(WU(y), { recursive: true }), await ec(y, w);
        if (x.length > 0) {
          let A = x.at(-1), U = fu(m, h + ".meta.json");
          await Qx(WU(U), { recursive: true });
          let { type: se, ...Le } = A;
          await QU(U, pe(Le), { mode: 384 });
        }
      }
    }
    return a;
  } catch (c) {
    throw await bg(a), c;
  }
}
function tw(e, t, r, o) {
  let { systemPrompt: n, settings: i, managedSettings: s, settingSources: a, sandbox: c, ...u } = e ?? {}, d, p, f;
  if (n === void 0) d = "";
  else if (typeof n === "string") d = n;
  else if (Array.isArray(n)) d = n;
  else if (n.type === "preset") p = n.append, f = n.excludeDynamicSections;
  process.env.CLAUDE_AGENT_SDK_VERSION = "0.3.179";
  let { abortController: m = qs(), additionalDirectories: g = [], agent: h, agents: y, allowedTools: v = [], betas: x, canUseTool: w, continue: A, cwd: U, debug: se, debugFile: Le, disallowedTools: Ye = [], tools: Lt, env: yt, executable: Qn = _u() ? "bun" : "node", executableArgs: Go = [], extraArgs: Rr = {}, fallbackModel: js, enableFileCheckpointing: cn, toolConfig: V, forkSession: mu, hooks: gu, includeHookEvents: Us, includePartialMessages: zs, forwardSubagentText: Ls, onElicitation: hu, onUserDialog: je, supportedDialogKinds: Ft, persistSession: Sr, sessionStore: $r, sessionStoreFlush: az, thinking: Fs, effort: cz, maxThinkingTokens: vg, maxTurns: lz, maxBudgetUsd: uz, taskBudget: dz, mcpServers: sw, model: pz, outputFormat: aw, permissionMode: fz = "default", allowDangerouslySkipPermissions: mz = false, permissionPromptToolName: gz, plugins: hz, getOAuthToken: cw, getHostAuthToken: lw, workload: uw, resume: dw, resumeSessionAt: yz, sessionId: bz, skills: pw, stderr: _z, strictMcpConfig: vz } = u;
  if ($r && Sr === false) throw Error("sessionStore cannot be used with persistSession: false -- the storage adapter requires local writes to mirror from. Use CLAUDE_CONFIG_DIR=/tmp for ephemeral local writes with external mirroring.");
  if (Ft !== void 0 && Ft.length > 0 && !je) throw Error("supportedDialogKinds requires an onUserDialog callback -- declaring dialog kinds without a handler would park dialogs nothing can answer. Provide onUserDialog, or omit supportedDialogKinds.");
  if ($r && A && !dw && !$r.listSessions) throw Error("Options.continue with sessionStore requires store.listSessions to be implemented");
  if ($r && cn) throw Error("enableFileCheckpointing is not yet supported with sessionStore (backup blobs are not mirrored, so rewindFiles() fails after a store-backed resume).");
  if ($r && u.spawnClaudeCodeProcess) ee("sessionStore with custom spawnClaudeCodeProcess: ensure the subprocess CLAUDE_CONFIG_DIR matches the parent (same path, same separators) or transcript_mirror frames will be dropped.", { level: "warn" });
  let Sg = u.pathToClaudeCodeExecutable;
  if (!Sg) {
    let $t = Zre(import.meta.url), nr = Bre($t), eo = LT((Jo) => nr.resolve(Jo));
    if (!eo) throw Error(`Native CLI binary for ${process.platform}-${process.arch} not found. Reinstall @anthropic-ai/claude-agent-sdk without --omit=optional, or set options.pathToClaudeCodeExecutable.`);
    Sg = eo;
  }
  let fw = aw?.type === "json_schema" ? aw.schema : void 0, ut = yt ? { ...yt } : { ...process.env };
  if (!ut.CLAUDE_CODE_ENTRYPOINT) ut.CLAUDE_CODE_ENTRYPOINT = "sdk-ts";
  if (!ut.CLAUDE_AGENT_SDK_VERSION) ut.CLAUDE_AGENT_SDK_VERSION = "0.3.179";
  if (cn) ut.CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING = "true";
  if (cw) ut.CLAUDE_CODE_SDK_HAS_OAUTH_REFRESH = "1";
  if (lw) ut.CLAUDE_CODE_SDK_HAS_HOST_AUTH_REFRESH = "1";
  if (V?.askUserQuestion?.previewFormat) ut.CLAUDE_CODE_QUESTION_PREVIEW_FORMAT = V.askUserQuestion.previewFormat;
  let xg = {};
  if (_g.propagation.inject(_g.context.active(), xg), "traceparent" in xg) {
    for (let $t of ["TRACEPARENT", "TRACESTATE"]) if (!($t in (yt ?? {}))) delete ut[$t];
  }
  for (let [$t, nr] of Object.entries(xg)) {
    let eo = $t.toUpperCase();
    if (!(eo in (yt ?? {}))) ut[eo] = nr;
  }
  let mw = {}, gw = /* @__PURE__ */ new Map();
  if (sw) for (let [$t, nr] of Object.entries(sw)) if (nr.type === "sdk" && nr.instance) gw.set($t, nr.instance);
  else mw[$t] = nr;
  let Bs;
  if (Fs) switch (Fs.type) {
    case "adaptive":
      Bs = { type: "adaptive", display: Fs.display };
      break;
    case "enabled":
      Bs = { type: "enabled", budgetTokens: Fs.budgetTokens, display: Fs.display };
      break;
    case "disabled":
      Bs = { type: "disabled" };
      break;
  }
  else if (vg !== void 0) Bs = vg === 0 ? { type: "disabled" } : { type: "enabled", budgetTokens: vg };
  if (r) {
    if (ut.CLAUDE_CONFIG_DIR = r, process.platform === "win32") ut.CLAUDE_SECURESTORAGE_CONFIG_DIR = yt?.CLAUDE_SECURESTORAGE_CONFIG_DIR ?? process.env.CLAUDE_SECURESTORAGE_CONFIG_DIR ?? yt?.CLAUDE_CONFIG_DIR ?? process.env.CLAUDE_CONFIG_DIR ?? "";
  }
  let hw = new Dh({ abortController: m, additionalDirectories: g, agent: h, betas: x, cwd: U, debug: se, debugFile: Le, executable: Qn, executableArgs: Go, extraArgs: uw ? { ...Rr, workload: uw } : Rr, pathToClaudeCodeExecutable: Sg, env: ut, forkSession: mu, stderr: _z, thinkingConfig: Bs, effort: cz, maxTurns: lz, maxBudgetUsd: uz, taskBudget: dz, model: pz, fallbackModel: js, jsonSchema: fw, permissionMode: fz, allowDangerouslySkipPermissions: mz, permissionPromptToolName: gz, continueConversation: $r ? void 0 : A, resume: dw, resumeSessionAt: yz, sessionId: bz, settings: typeof i === "object" ? pe(i) : i, managedSettings: s ? pe(s) : void 0, settingSources: a, skills: pw, allowedTools: v, disallowedTools: Ye, tools: Lt, mcpServers: mw, strictMcpConfig: vz, canUseTool: !!w, hooks: !!gu, includeHookEvents: Us, includePartialMessages: zs, persistSession: Sr, sessionMirror: !!$r, plugins: hz, sandbox: c, spawnClaudeCodeProcess: u.spawnClaudeCodeProcess, deferSpawn: o }), Sz = { systemPrompt: d, appendSystemPrompt: p, planModeInstructions: u.planModeInstructions, appendSubagentSystemPrompt: u.appendSubagentSystemPrompt, toolAliases: u.toolAliases, excludeDynamicSections: f, agents: y, title: u.title, skills: pw, webSearchIsolationExemptMcpServers: u.webSearchIsolationExemptMcpServers, promptSuggestions: u.promptSuggestions, agentProgressSummaries: u.agentProgressSummaries, forwardSubagentText: Ls, supportedDialogKinds: Ft }, wg = new zh(hw, t, w, gu, m, gw, fw, Sz, hu, cw, lw, je);
  if ($r) {
    let $t = () => zt(ut.CLAUDE_CONFIG_DIR ?? zt(ew(), ".claude"), "projects"), nr = az === "eager", eo = new Lh(async (Jo, kg) => {
      let Hs = XU(Jo, $t());
      if (Hs) await $r.append(Hs, kg);
      else ee(`[SessionStore] dropping mirror frame: filePath ${Jo} is not under ${$t()} -- subprocess CLAUDE_CONFIG_DIR likely differs from parent (custom spawnClaudeCodeProcess / container?)`, { level: "warn" });
    }, void 0, (Jo, kg) => {
      let Hs = XU(Jo, $t());
      if (Hs) wg.reportMirrorError(Hs, kg.message);
    }, nr ? 0 : vd, nr ? 0 : Sd);
    wg.setTranscriptMirrorBatcher(eo);
  }
  return { queryInstance: wg, transport: hw, abortController: m, processEnv: ut };
}
function rw(e, t, r, o) {
  if (typeof r === "string") t.write(pe({ type: "user", session_id: "", message: { role: "user", content: [{ type: "text", text: r }] }, parent_tool_use_id: null }) + `
`);
  else e.streamInput(r).catch((n) => o.abort(n));
}
var Gre = /* @__PURE__ */ new Set(["EBUSY", "EMFILE", "ENFILE", "ENOTEMPTY", "EPERM"]);
async function bg(e) {
  for (let t = 0; ; t++) try {
    return await Fre(e, { recursive: true, force: true });
  } catch (r) {
    if (t >= 4 || !Gre.has(Ge(r) ?? "")) return;
    await yu((t + 1) * 100);
  }
}
function Jre(e, t) {
  e.waitForExit().catch(() => {
  }).finally(() => bg(t));
}
function ECe({ prompt: e, options: t }) {
  if ((t?.resume || t?.continue) && t?.sessionStore) {
    let { queryInstance: i, transport: s, abortController: a, processEnv: c } = tw({ ...t }, typeof e === "string", void 0, true), u = fu(t.cwd ?? "."), d = t.sessionStore, p = t.loadTimeoutMs ?? 6e4, f = t.resume;
    return (async () => {
      if (!f) f = (await Ar(d.listSessions(rr(u)), p, `SessionStore.listSessions() timed out after ${p}ms`)).slice().sort((h, y) => y.mtime - h.mtime)[0]?.sessionId;
      if (!f) return;
      return rz(d, f, u, t.env, t.loadTimeoutMs);
    })().then((g) => {
      if (g) {
        s.updateResume(f);
        let h = { CLAUDE_CONFIG_DIR: g };
        if (process.platform === "win32") {
          let y = t.env?.CLAUDE_SECURESTORAGE_CONFIG_DIR ?? process.env.CLAUDE_SECURESTORAGE_CONFIG_DIR ?? t.env?.CLAUDE_CONFIG_DIR ?? process.env.CLAUDE_CONFIG_DIR ?? "";
          h.CLAUDE_SECURESTORAGE_CONFIG_DIR = y, c.CLAUDE_SECURESTORAGE_CONFIG_DIR = y;
        }
        s.updateEnv(h), c.CLAUDE_CONFIG_DIR = g, i.addCleanupCallback(() => Jre(s, g));
      }
      if (!i.isClosed()) s.spawn();
    }).catch((g) => {
      let h = kr(g);
      s.spawnAbort(h), i.setError(h);
    }), rw(i, s, e, a), i;
  }
  let { queryInstance: r, transport: o, abortController: n } = tw(t, typeof e === "string");
  return rw(r, o, e, n), r;
}
function nz(e) {
  let t = fu(e ?? "."), r;
  try {
    r = Ure(t);
  } catch {
    r = t;
  }
  return Or(r);
}
function rr(e) {
  return So(nz(e));
}
function nw(e) {
  return typeof e === "object" && e !== null && "type" in e && e.type === "agent_metadata";
}
function XU(e, t) {
  let r = tz(t, e), o = r.split(iw);
  if (o[0] === ".." || ez(r)) return null;
  if (o.length < 2) return null;
  let n = o[0], i = o[1];
  if (o.length === 2 && i.endsWith(".jsonl")) return { projectKey: n, sessionId: i.replace(/\.jsonl$/, "") };
  if (o.length >= 4) {
    let s = o.slice(2), a = s.length - 1;
    return s[a] = s.at(-1).replace(/\.jsonl$/, ""), { projectKey: n, sessionId: i, subpath: s.join("/") };
  }
  return null;
}

// sidecar.mjs
import * as readline from "node:readline";
function emit(obj) {
  process.stdout.write(JSON.stringify(obj) + "\n");
}
function log(msg, ...args) {
  process.stderr.write(`[sidecar] ${msg} ${args.map(String).join(" ")}
`);
}
var pendingQuestions = /* @__PURE__ */ new Map();
function parkQuestion(toolUseId, questions) {
  return new Promise((resolve, reject) => {
    pendingQuestions.set(toolUseId, { resolve, reject });
    emit({
      v: 1,
      kind: "ask_user_question",
      toolUseId,
      questions
    });
  });
}
function deliverAnswer(frame) {
  const { toolUseId, answers, response, annotations } = frame;
  const pending = pendingQuestions.get(toolUseId);
  if (!pending) {
    log("warn: received answer for unknown toolUseId", toolUseId);
    return;
  }
  pendingQuestions.delete(toolUseId);
  const updatedInput = { questions: frame.questions || [], answers: answers || {} };
  if (response !== void 0) updatedInput.response = response;
  if (annotations !== void 0) updatedInput.annotations = annotations;
  pending.resolve({
    behavior: "allow",
    updatedInput
  });
}
async function canUseTool(tool) {
  if (tool.name === "AskUserQuestion") {
    const input = tool.input;
    const questions = input.questions || [];
    const toolUseId = tool.id || `ask-${Date.now()}`;
    try {
      return await parkQuestion(toolUseId, questions);
    } catch (err) {
      log("error: AskUserQuestion promise rejected", err);
      return { behavior: "allow", updatedInput: tool.input };
    }
  }
  return { behavior: "allow" };
}
function createPromptGenerator() {
  let resolveNext = null;
  let nextMessage = null;
  let closed = false;
  const generator = {
    [Symbol.asyncIterator]() {
      return this;
    },
    async next() {
      if (closed && nextMessage === null) {
        return { done: true, value: void 0 };
      }
      if (nextMessage !== null) {
        const msg = nextMessage;
        nextMessage = null;
        return { done: false, value: msg };
      }
      return new Promise((resolve) => {
        resolveNext = resolve;
      });
    },
    return() {
      closed = true;
      if (resolveNext) {
        resolveNext({ done: true, value: void 0 });
        resolveNext = null;
      }
      return Promise.resolve({ done: true, value: void 0 });
    }
  };
  const send = (message) => {
    nextMessage = message;
    if (resolveNext) {
      const resolve = resolveNext;
      resolveNext = null;
      const msg = nextMessage;
      nextMessage = null;
      resolve({ done: false, value: msg });
    }
  };
  const close = () => {
    closed = true;
    if (resolveNext) {
      resolveNext({ done: true, value: void 0 });
      resolveNext = null;
    }
  };
  return { generator, send, close };
}
function processMessage(msg) {
  switch (msg.type) {
    case "assistant": {
      const content = msg.message?.content || [];
      for (const block of content) {
        if (block.type === "text") {
          emit({ v: 1, kind: "token", text: block.text });
        } else if (block.type === "thinking") {
          emit({
            v: 1,
            kind: "thinking",
            text: block.thinking || "",
            truncated: false,
            redacted: false
          });
        } else if (block.type === "redacted_thinking") {
          emit({
            v: 1,
            kind: "thinking",
            text: "",
            truncated: false,
            redacted: true
          });
        } else if (block.type === "tool_use") {
          if (block.name !== "AskUserQuestion") {
            emit({
              v: 1,
              kind: "tool_use",
              toolName: block.name,
              toolInput: JSON.stringify(block.input || {})
            });
          }
        }
      }
      break;
    }
    case "user": {
      const content = msg.message?.content || [];
      for (const block of content) {
        if (block.type === "tool_result") {
          const toolContent = Array.isArray(block.content) ? block.content.map((c) => c.type === "text" ? c.text : "").join("") : String(block.content || "");
          emit({
            v: 1,
            kind: "tool_result",
            toolName: "",
            toolOutput: toolContent,
            isError: block.is_error || false
          });
        }
      }
      break;
    }
    case "result": {
      emit({
        v: 1,
        kind: "summary",
        totalCostUsd: msg.total_cost_usd || 0,
        usage: msg.usage || {}
      });
      break;
    }
    case "system":
      break;
    // stream_event partials (includePartialMessages:true): ignore deltas here
    // because the assistant/user/result messages carry the completed blocks.
    case "stream_event":
      break;
    default:
      log("unknown SDK message type", msg.type);
  }
}
async function runQueryLoop(generator, options) {
  for await (const msg of ECe({ prompt: generator, options })) {
    try {
      processMessage(msg);
    } catch (err) {
      log("error processing message", err);
      emit({ v: 1, kind: "error", text: String(err) });
    }
  }
}
function startStdinReader(onUserTurn, onAnswer, onShutdown) {
  return new Promise((resolve) => {
    const rl2 = readline.createInterface({
      input: process.stdin,
      crlfDelay: Infinity
    });
    rl2.on("line", (line) => {
      const trimmed = line.trim();
      if (!trimmed) return;
      let frame;
      try {
        frame = JSON.parse(trimmed);
      } catch (err) {
        log("warn: invalid JSON on stdin:", trimmed.slice(0, 200));
        return;
      }
      if (frame.v !== 1) {
        log("warn: unsupported protocol version", frame.v);
        return;
      }
      switch (frame.kind) {
        case "user_turn":
          onUserTurn(frame.text || "");
          break;
        case "answer":
          onAnswer(frame);
          break;
        case "shutdown":
          rl2.close();
          resolve();
          break;
        default:
          log("warn: unknown frame kind", frame.kind);
      }
    });
    rl2.on("close", () => {
      resolve();
    });
    rl2.on("error", (err) => {
      log("stdin error:", err);
      resolve();
    });
  });
}
async function main() {
  const options = {
    permissionMode: "bypassPermissions",
    includePartialMessages: true,
    apiKeySource: "none",
    canUseTool
  };
  for (let i = 2; i < process.argv.length; i++) {
    if (process.argv[i] === "--model" && process.argv[i + 1]) {
      options.model = process.argv[i + 1];
      i++;
    }
  }
  const { generator, send: sendToModel, close: closeGenerator } = createPromptGenerator();
  emit({ v: 1, kind: "ready" });
  const queryPromise = runQueryLoop(generator, options).catch((err) => {
    log("query loop error:", err);
    emit({ v: 1, kind: "error", text: String(err) });
  });
  await startStdinReader(
    // user_turn: inject into the generator.
    (text) => {
      sendToModel({
        role: "user",
        content: [{ type: "text", text }]
      });
    },
    // answer: deliver to pending question promise.
    (frame) => {
      deliverAnswer(frame);
    },
    // shutdown: close generator.
    () => {
      closeGenerator();
    }
  );
  closeGenerator();
  await queryPromise;
  log("sidecar exiting");
}
main().catch((err) => {
  process.stderr.write(`[sidecar] fatal: ${err}
`);
  process.exit(1);
});
