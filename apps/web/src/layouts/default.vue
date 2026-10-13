<template>
  <div class="page-view">
    <div class="page-content">
      <SiteHeader />
      <slot />
    </div>
    <SiteFooter />
    <el-backtop :bottom="50" :right="40" />
  </div>
</template>
