<template>
  <footer class="site-footer">
    <div class="footer_content">
      <div class="footer_left">
        <img src="~/assets/images/logo-bottom.png" alt="malxchange" />
        <p>Trade every listed Coin on one open Market.</p>
        <p>Copyright © 2026 malxchange. All rights reserved.</p>
      </div>
      <div class="footer_right">
        <ul>
          <li class="footer_title"><span>Exchange</span></li>
          <li><NuxtLink to="/exchange">Trade a Market</NuxtLink></li>
          <li><NuxtLink to="/help">How to place an order</NuxtLink></li>
          <li><NuxtLink to="/helplist">Coin guides</NuxtLink></li>
        </ul>
        <ul>
          <li class="footer_title"><span>Content</span></li>
          <li><NuxtLink to="/notice">Announcements</NuxtLink></li>
          <li><NuxtLink to="/help">Help center</NuxtLink></li>
          <li><NuxtLink to="/helplist">Frequently asked questions</NuxtLink></li>
        </ul>
        <ul>
          <li class="footer_title"><span>Member</span></li>
          <li><NuxtLink to="/register">Create an account</NuxtLink></li>
          <li><NuxtLink to="/login">Log in</NuxtLink></li>
          <li><NuxtLink to="/uc/safe">Security settings</NuxtLink></li>
        </ul>
      </div>
    </div>
  </footer>
</template>

<script setup lang="ts">
// Ported footer chrome from the legacy App.vue: #192330 band, bottom
// logo, company line, and link columns. The legacy footer's cut surface —
// social QR tooltips, Chinese media links, app download, and
// whitepaper/about links — is gone; the columns keep only destinations
// this port serves.
</script>

<style scoped lang="scss">
.site-footer {
  .footer_content {
    min-height: 300px;
    padding: 80px 10%;
    color: rgba(255, 255, 255, 0.8);
    background: var(--panel-bg);
    overflow: hidden;
  }

  .footer_left {
    float: left;
    font-size: 14px;
    margin-right: 5%;

    img {
      margin: 15px 0;
      max-width: 300px;
    }

    p {
      margin: 10px 0;
      color: var(--text-secondary);
    }
  }

  .footer_right {
    float: right;
    text-align: left;
    border-left: 1px solid #243051;
    padding-left: 5%;

    ul {
      float: left;
      margin: 0 30px;
      padding: 0;
    }

    li {
      list-style-type: none;
    }

    li a {
      color: var(--text-secondary);
      line-height: 30px;
      display: block;
      font-size: 12px;

      &:hover {
        color: var(--text);
      }
    }

    .footer_title {
      font-size: 13px;
      height: 40px;
    }
  }
}

@media screen and (max-width: 768px) {
  .footer_content {
    padding: 70px 2% 85px 5%;
  }

  .footer_right {
    display: none;
  }
}
</style>
