<template>
  <div class="login_form">
    <div class="login_right">
      <h1 class="auth-title">{{ title }}</h1>
      <slot />
    </div>
  </div>
</template>

<script setup lang="ts">
// Ported auth card chrome from the legacy Login/Register/FindPwd pages:
// the centered #17212e panel with the gold top border on the #0b1520
// backdrop, shared by every sign-in page. The underline-style inputs and
// the gold submit button ride along here so all three pages agree.
defineProps<{ title: string }>()
</script>

<style scoped lang="scss">
.login_form {
  background: #f8fafc;
  min-height: 600px;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 60px 20px;

  .login_right {
    width: 400px;
    max-width: 100%;
    background: #ffffff;
    border: 1px solid #e2e8f0;
    border-radius: 12px;
    padding: 40px;
    box-shadow: 0 4px 6px rgba(0, 0, 0, 0.05);
  }
}

.auth-title {
  color: #1e293b;
  font-size: 28px;
  font-weight: 700;
  text-align: center;
  margin: 0 0 8px;
}

.auth-subtitle {
  color: #64748b;
  font-size: 15px;
  text-align: center;
  margin-bottom: 32px;
}

// Clean boxed inputs for light theme
:deep(.el-form-item) {
  margin-bottom: 20px;
}

:deep(.el-input__wrapper) {
  background: #ffffff;
  border: 1px solid #e2e8f0;
  border-radius: 8px;
  box-shadow: none;
  padding: 4px 12px;

  &:hover {
    border-color: #cbd5e1;
  }

  &.is-focus {
    border-color: #1e293b;
    box-shadow: 0 0 0 3px rgba(30, 41, 59, 0.1);
  }
}

:deep(.el-input__inner) {
  color: #1e293b;
  font-size: 15px;
  height: 40px;

  &::placeholder {
    color: #94a3b8;
  }
}

:deep(.el-form-item__error) {
  color: #dc2626;
}

// Black full-width submit button
:deep(.auth-submit) {
  width: 100%;
  height: 48px;
  background-color: #1e293b;
  border-color: #1e293b;
  color: #fff;
  font-size: 16px;
  font-weight: 600;
  border-radius: 8px;
  margin-top: 8px;

  &:hover,
  &:focus {
    background-color: #0f172a;
    border-color: #0f172a;
    color: #fff;
  }
}

// The inline "send code" trigger next to the code field.
:deep(.send-code) {
  position: absolute;
  top: -10px;
  right: 0;
  width: 34%;
  height: 30px;
  padding: 0;
  background: transparent;
  border: 1px solid var(--accent);
  border-radius: 5px;
  color: var(--accent);

  &:not(:disabled):hover,
  &:not(:disabled):focus {
    background: transparent;
    border-color: var(--accent-border);
    color: var(--accent-border);
  }

  &:disabled {
    border-color: var(--text-muted);
    color: var(--text-muted);
    cursor: default;
  }
}

// Status sentences: api errors red, confirmations green, both complete
// sentences straight from the copy contract.
:deep(.auth-error) {
  margin: 0 0 12px;
}

:deep(.auth-status) {
  margin: 0 0 12px;
  font-size: 12px;
  line-height: 18px;
}

:deep(.auth-status--ok) {
  color: var(--buy-green);
}

:deep(.auth-status--wait) {
  color: var(--text-secondary);
}
</style>
