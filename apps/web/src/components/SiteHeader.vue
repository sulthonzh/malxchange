<template>
  <div class="layout">
    <div class="layout-ceiling">
      <NuxtLink to="/" class="layout-logo" aria-label="Go to the home page">
        <img src="~/assets/images/logo.png" alt="malxchange" />
      </NuxtLink>
      <div class="layout-ceiling-main">
        <nav class="header_nav">
          <NuxtLink v-for="item in navItems" :key="item.to" :to="item.to">
            {{ item.label }}
          </NuxtLink>
        </nav>
        <button
          class="header_nav_mobile_triggle"
          type="button"
          aria-label="Open the navigation menu"
          @click="drawerOpen = true"
        >
          <el-icon :size="26" color="#cccccc"><Menu /></el-icon>
        </button>
        <div class="rr login-container">
          <template v-if="session.loggedIn.value">
            <div class="mymsg">
              <NuxtLink to="/uc/safe">Member center</NuxtLink>
            </div>
            <el-dropdown>
              <a class="member-trigger" href="javascript:void(0)">
                <el-icon :size="18"><User /></el-icon>
                <span>{{ session.member.value?.email ?? 'Member' }}</span>
                <el-icon :size="14"><ArrowDown /></el-icon>
              </a>
              <template #dropdown>
                <el-dropdown-menu>
                  <el-dropdown-item>
                    <NuxtLink to="/uc/money">Assets</NuxtLink>
                  </el-dropdown-item>
                  <el-dropdown-item>
                    <NuxtLink to="/uc/entrust/current">Orders</NuxtLink>
                  </el-dropdown-item>
                  <el-dropdown-item divided @click="logout">
                    Log out
                  </el-dropdown-item>
                </el-dropdown-menu>
              </template>
            </el-dropdown>
          </template>
          <div v-else class="login_register">
            <NuxtLink id="login" to="/login">Log in</NuxtLink>
            <NuxtLink id="register" to="/register">Sign up</NuxtLink>
          </div>
        </div>
      </div>
    </div>
  </div>

  <el-drawer v-model="drawerOpen" title="Navigation" direction="rtl" size="40%">
    <nav class="drawer_nav">
      <NuxtLink v-for="item in navItems" :key="item.to" :to="item.to" @click="drawerOpen = false">
        {{ item.label }}
      </NuxtLink>
      <NuxtLink to="/login" @click="drawerOpen = false">Log in</NuxtLink>
      <NuxtLink to="/register" @click="drawerOpen = false">Sign up</NuxtLink>
    </nav>
  </el-drawer>
</template>

<script setup lang="ts">
import { ArrowDown, Menu, User } from '@element-plus/icons-vue'

// Ported header chrome from the legacy App.vue: dark #172636 bar, logo,
// left navigation, and login/register or the member menu on the right.
// The legacy nav's cut destinations (OTC, CTC, lab, invite, whitepaper,
// app download, language switcher) are gone — the port keeps Home,
// Exchange, Announcements, and Help.
const navItems = [
  { to: '/', label: 'Home' },
  { to: '/exchange', label: 'Exchange' },
  { to: '/notice', label: 'Announcements' },
  { to: '/help', label: 'Help' },
] as const

const session = useSession()
const drawerOpen = ref(false)

function logout(): void {
  session.clearSession()
}
</script>

<style scoped lang="scss">
.layout {
  width: 100%;
  position: absolute;
  z-index: 10;

  .layout-ceiling {
    padding: 5px 20px;
    background: var(--site-header-bg);
    box-shadow: 0 0 5px 5px rgba(0, 0, 0, 0.1);

    .layout-logo {
      width: 218px;
      height: 48px;
      display: inline-flex;
      align-items: center;
      float: left;

      img {
        max-height: 48px;
      }
    }

    .layout-ceiling-main {
      height: 50px;
      line-height: 50px;
      margin-left: 218px;

      .header_nav {
        float: left;

        a {
          color: var(--text);
          display: inline-block;
          line-height: 40px;
          height: 40px;
          text-align: center;
          margin-left: 20px;

          &:hover {
            color: var(--accent);
          }

          &.router-link-active {
            color: var(--accent);
            border-bottom: 3px solid var(--accent-border);
          }
        }
      }

      .rr {
        float: right;
        z-index: 10;

        .mymsg {
          float: left;
          padding-right: 20px;

          a {
            color: var(--text-secondary);
            display: inline;
            padding-right: 20px;
            border-right: 1px solid var(--text-secondary);

            &:hover {
              color: var(--text);
            }
          }
        }

        .member-trigger {
          color: var(--text-secondary);
          display: inline-flex;
          align-items: center;
          gap: 4px;
          margin-left: 20px;

          &:hover {
            color: var(--text);
          }
        }

        .login_register {
          float: left;
          padding-right: 20px;
          border-right: 1px solid #273c55;

          #login {
            color: var(--text-secondary);
            margin-left: 0;
            border-right: 1px solid #273c55;

            &:hover {
              color: var(--text);
            }
          }

          #register {
            color: var(--accent);
            margin-left: 0;

            &:hover {
              color: var(--text);
            }
          }
        }
      }
    }
  }
}

.header_nav_mobile_triggle {
  display: none;
  float: right;
  padding: 0 5px 0 20px;
  background: none;
  border: none;
  cursor: pointer;
  line-height: 50px;
}

.drawer_nav {
  display: flex;
  flex-direction: column;
  gap: 8px;

  a {
    color: var(--text-secondary);
    text-align: left;

    &:hover {
      color: var(--accent);
    }
  }
}

@media screen and (max-width: 768px) {
  .header_nav,
  .login-container .mymsg {
    display: none;
  }

  .header_nav_mobile_triggle {
    display: inline-block;
  }

  .layout .layout-ceiling {
    padding: 5px 10px;

    .layout-logo {
      width: 200px;

      img {
        max-height: 35px;
      }
    }

    .layout-ceiling-main {
      height: 35px;
      line-height: 35px;
      margin-left: 0;
    }
  }
}
</style>
