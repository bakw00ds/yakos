// setup.js — first-admin setup form handler (ADR-0005 Phase 3c).
// Loaded from /setup.js under script-src 'self'; no inline scripts.
(function () {
  'use strict';

  var form = document.getElementById('setupForm');
  var btn = document.getElementById('btn');
  var errEl = document.getElementById('err');
  var successNote = document.getElementById('successNote');

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

    var token = document.getElementById('token').value;
    var username = document.getElementById('username').value;
    var password = document.getElementById('password').value;
    var confirm = document.getElementById('confirm').value;

    if (password !== confirm) {
      showError('Passwords do not match.');
      return;
    }

    btn.disabled = true;
    btn.textContent = 'Creating account…';

    fetch('/setup', {
      method: 'POST',
      headers: {'Content-Type': 'application/json'},
      credentials: 'same-origin',
      body: JSON.stringify({token: token, username: username, password: password})
    })
      .then(function (res) {
        if (res.status === 403) {
          showError('Invalid or expired setup token. Run `yakos console bootstrap-token` to regenerate.');
          btn.disabled = false;
          btn.textContent = 'Create admin account';
          return null;
        }
        if (res.status === 409) {
          showError('Setup already complete. Please sign in.');
          setTimeout(function () { window.location.href = '/login'; }, 1500);
          return null;
        }
        if (res.status === 400) {
          return res.json().then(function (data) {
            showError(data.error || 'Invalid input.');
            btn.disabled = false;
            btn.textContent = 'Create admin account';
            return null;
          });
        }
        if (!res.ok) {
          showError('Setup failed (' + res.status + '). Please try again.');
          btn.disabled = false;
          btn.textContent = 'Create admin account';
          return null;
        }
        return res.json();
      })
      .then(function (data) {
        if (!data) return;
        successNote.style.display = 'block';
        setTimeout(function () { window.location.href = '/login'; }, 1500);
      })
      .catch(function () {
        showError('Network error. Please try again.');
        btn.disabled = false;
        btn.textContent = 'Create admin account';
      });
  });
}());
