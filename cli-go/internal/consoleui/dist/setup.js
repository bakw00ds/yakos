// setup.js — first-admin setup form handler (ADR-0005 Phase 3c).
// Loaded from /setup.js under script-src 'self'; no inline scripts.
//
// Handles:
//   200 { ok: true }              — redirect to /login
//   400 { error: "..." }          — display server error; re-enable button
//   403 { error: "..." }          — display server error; re-enable button
//   409 { error: "setup..." }     — redirect to /login after brief delay
//   429 { error: "too many..." }  — display rate-limit message; re-enable button
//   other non-2xx                 — display generic error with status code
//   network error                 — display network error message
//
// Accessibility:
//   - The error region (#err) has role="alert" (aria-live="assertive");
//     screen readers announce errors immediately.
//   - On error, focus moves to the first empty relevant field so keyboard
//     users don't have to hunt.
//   - novalidate on the form keeps browser-default validation bubbles out of
//     the way so we control error presentation consistently across UAs.
(function () {
  'use strict';

  var form       = document.getElementById('setupForm');
  var btn        = document.getElementById('btn');
  var errEl      = document.getElementById('err');
  var successNote = document.getElementById('successNote');
  var tokenEl    = document.getElementById('token');
  var usernameEl = document.getElementById('username');
  var passwordEl = document.getElementById('password');
  var confirmEl  = document.getElementById('confirm');

  function showError(msg) {
    errEl.textContent = msg;
    errEl.style.display = 'block';
    // Return focus to the most relevant empty field so keyboard/SR users
    // know where to correct the input.
    if (tokenEl && !tokenEl.value.trim()) {
      tokenEl.focus();
    } else if (usernameEl && !usernameEl.value.trim()) {
      usernameEl.focus();
    } else if (passwordEl) {
      passwordEl.value = '';
      if (confirmEl) { confirmEl.value = ''; }
      passwordEl.focus();
    }
  }

  function clearError() {
    errEl.style.display = 'none';
    errEl.textContent = '';
  }

  function resetBtn() {
    btn.disabled = false;
    btn.textContent = 'Create admin account';
  }

  form.addEventListener('submit', function (e) {
    e.preventDefault();
    clearError();

    var token    = tokenEl ? tokenEl.value : '';
    var username = usernameEl ? usernameEl.value : '';
    var password = passwordEl ? passwordEl.value : '';
    var confirm  = confirmEl ? confirmEl.value : '';

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
        if (res.status === 429) {
          // Rate-limited: the server returns {"error":"too many requests"}.
          // Display the server message so the operator knows to wait.
          return res.json().then(function (data) {
            showError(data.error || 'Too many requests. Please wait a few minutes and try again.');
            resetBtn();
            return null;
          }).catch(function () {
            showError('Too many requests. Please wait a few minutes and try again.');
            resetBtn();
            return null;
          });
        }
        if (res.status === 403) {
          return res.json().then(function (data) {
            showError(data.error || 'Invalid or expired setup token. Run `yakos console bootstrap-token` to regenerate.');
            resetBtn();
            return null;
          }).catch(function () {
            showError('Invalid or expired setup token. Run `yakos console bootstrap-token` to regenerate.');
            resetBtn();
            return null;
          });
        }
        if (res.status === 409) {
          return res.json().then(function (data) {
            showError(data.error || 'Setup already complete. Please sign in.');
            setTimeout(function () { window.location.href = '/login'; }, 1500);
            return null;
          }).catch(function () {
            showError('Setup already complete. Redirecting to sign in…');
            setTimeout(function () { window.location.href = '/login'; }, 1500);
            return null;
          });
        }
        if (res.status === 400) {
          return res.json().then(function (data) {
            showError(data.error || 'Invalid input. Please check your entries and try again.');
            resetBtn();
            return null;
          }).catch(function () {
            showError('Invalid input. Please check your entries and try again.');
            resetBtn();
            return null;
          });
        }
        if (!res.ok) {
          showError('Setup failed (' + res.status + '). Please try again.');
          resetBtn();
          return null;
        }
        return res.json();
      })
      .then(function (data) {
        if (!data) return;
        // Success: show confirmation note then redirect to /login.
        successNote.style.display = 'block';
        setTimeout(function () { window.location.href = '/login'; }, 1500);
      })
      .catch(function () {
        showError('Network error. Please check your connection and try again.');
        resetBtn();
      });
  });
}());
