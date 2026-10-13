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
  background: var(--body-bg) url('~/assets/images/login_bg.png') no-repeat center center;
  height: 760px;
  position: relative;
  overflow: hidden;

  .login_right {
    position: absolute;
    left: 50%;
    top: 50%;
    width: 350px;
    background: #17212e;
    border-top: 4px solid var(--accent);
    border-radius: 5px;
    padding: 20px 30px;
    transform: translate(-50%, -50%);
  }
}

.auth-title {
  color: var(--text);
  font-size: 25px;
  font-weight: normal;
  text-align: center;
  height: 70px;
  line-height: 70px;
  margin: 0;
}

// Underline-style form controls ported from the legacy pages: a bare
// bottom border that lights up gold on focus, no box, no radius.
:deep(.el-form-item) {
  margin-bottom: 22px;
}

:deep(.el-input__wrapper) {
  background: transparent;
  border-radius: 0;
  box-shadow: 0 1px 0 0 var(--panel-border);
  padding: 1px 0;

  &.is-focus {
    box-shadow: 0 1px 0 0 var(--accent);
  }
}

:deep(.el-input__inner) {
  color: var(--text);
  font-size: 14px;

  &::placeholder {
    color: var(--text-muted);
  }
}

:deep(.el-form-item__error) {
  color: var(--sell-red);
}

// The gold full-width submit every legacy auth form used.
:deep(.auth-submit) {
  width: 100%;
  height: 42px;
  background-color: var(--accent);
  border-color: var(--accent);
  color: #fff;
  font-size: 18px;
  border-radius: 5px;
  margin-top: 10px;

  &:hover,
  &:focus {
    background-color: var(--accent-border);
    border-color: var(--accent-border);
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
