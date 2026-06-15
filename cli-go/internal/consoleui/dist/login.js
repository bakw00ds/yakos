// login.js — minimal login form handler for yakOS Phase 3b.
// Loaded from /login.js under script-src 'self'; no inline scripts.
// CSP: script-src 'self' — no eval, no blob workers, no CDN.
(function () {
  'use strict';

  var form = document.getElementById('loginForm');
  var btn = document.getElementById('btn');
  var errEl = document.getElementById('err');
  var resetNote = document.getElementById('resetNote');

  function showError(msg) {
    errEl.textContent = msg;
    errEl.style.display = 'block';
  }

  function clearError() {
    errEl.style.display = 'none';
    errEl.textContent = '';
  }

  form.addEventListener('submit', function (e) {
    e.preventDefault();
    clearError();
    btn.disabled = true;
    btn.textContent = 'Signing in…';

    var username = document.getElementById('username').value;
    var password = document.getElementById('password').value;

    fetch('/login', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      credentials: 'same-origin',
      body: JSON.stringify({username: username, password: password})
    })
      .then(function (res) {
        if (res.status === 429) {
          showError('Too many attempts. Please wait and try again.');
          btn.disabled = false;
          btn.textContent = 'Sign in';
          return null;
        }
        if (res.status === 401) {
          showError('Invalid username or password.');
          btn.disabled = false;
          btn.textContent = 'Sign in';
          return null;
        }
        if (!res.ok) {
          showError('Sign-in failed (' + res.status + '). Please try again.');
          btn.disabled = false;
          btn.textContent = 'Sign in';
          return null;
        }
        return res.json();
      })
      .then(function (data) {
        if (!data) return;
        if (data.passwordResetRequired) {
          // Show reset note; redirect to / (change-password flow is Phase 3c+).
          resetNote.style.display = 'block';
          setTimeout(function () { window.location.href = '/'; }, 1500);
          return;
        }
        window.location.href = '/';
      })
      .catch(function () {
        showError('Network error. Please try again.');
        btn.disabled = false;
        btn.textContent = 'Sign in';
      });
  });
}());
