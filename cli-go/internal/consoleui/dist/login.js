// login.js — login form handler for yakOS Phase 3d.
//
// Loaded from /login.js under script-src 'self'; no inline scripts.
// CSP: script-src 'self' — no eval, no blob workers, no CDN.
//
// Handles:
//   200 { ok: true }                    — redirect to /
//   200 { passwordResetRequired: true } — show reset note
//   401                                 — show generic invalid credentials
//   429                                 — show rate-limit message
//   other                               — show generic error with status code
//   network error                       — show network error message
//
// Accessibility:
//   - The error region (#login-err) is aria-live="assertive"; updates are
//     announced by screen readers immediately.
//   - On error, focus returns to the first empty field (username if blank,
//     otherwise password) so keyboard users don't have to hunt.
//   - The form uses novalidate so we control error presentation (no browser
//     default tooltip bubbles which vary by UA and can confuse SR users).
//   - The reset note is aria-live="polite" and displayed via the .visible
//     class (not inline style) so no CSP-inline-style is required.
(function () {
  'use strict';

  var form      = document.getElementById('loginForm');
  var btn       = document.getElementById('btn');
  var errEl     = document.getElementById('login-err');
  var resetNote = document.getElementById('resetNote');
  var usernameEl = document.getElementById('username');
  var passwordEl = document.getElementById('password');

  function showError(msg) {
    errEl.textContent = msg;
    errEl.classList.add('visible');
    // Return focus to the most appropriate field.
    if (usernameEl && !usernameEl.value.trim()) {
      usernameEl.focus();
    } else if (passwordEl) {
      passwordEl.value = '';
      passwordEl.focus();
    }
  }

  function clearError() {
    errEl.classList.remove('visible');
    errEl.textContent = '';
  }

  function setSubmitting(submitting) {
    btn.disabled = submitting;
    btn.textContent = submitting ? 'Signing in…' : 'Sign in';
  }

  form.addEventListener('submit', function (e) {
    e.preventDefault();
    clearError();

    var username = usernameEl ? usernameEl.value.trim() : '';
    var password = passwordEl ? passwordEl.value : '';

    // Client-side blank-field check: gives immediate feedback without a
    // round-trip.  The server also validates; this is convenience only.
    if (!username) {
      showError('Username is required.');
      return;
    }
    if (!password) {
      showError('Password is required.');
      return;
    }

    setSubmitting(true);

    fetch('/login', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'same-origin',
      body: JSON.stringify({ username: username, password: password }),
    })
      .then(function (res) {
        if (res.status === 429) {
          setSubmitting(false);
          showError('Too many sign-in attempts. Please wait a few minutes and try again.');
          return null;
        }
        if (res.status === 401) {
          setSubmitting(false);
          // Generic message — never reveal which field (username vs password) is wrong.
          showError('Invalid username or password.');
          return null;
        }
        if (!res.ok) {
          setSubmitting(false);
          showError('Sign-in failed (' + res.status + '). Please try again.');
          return null;
        }
        return res.json();
      })
      .then(function (data) {
        if (!data) return;
        if (data.passwordResetRequired) {
          // Show reset note; the change-password flow is a later phase.
          // Keep the button disabled to prevent a re-submit while the note
          // is visible; the page will eventually redirect.
          resetNote.classList.add('visible');
          setTimeout(function () { window.location.href = '/'; }, 2000);
          return;
        }
        // Success: redirect to console root.
        window.location.href = '/';
      })
      .catch(function () {
        setSubmitting(false);
        showError('Network error. Please check your connection and try again.');
      });
  });

  // Auto-focus the username field on page load for keyboard users.
  if (usernameEl) {
    usernameEl.focus();
  }
}());
