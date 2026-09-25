<template>
  <img v-if="src && !failed"
       :src="src"
       :alt="alt"
       :class="className"
       :style="style"
       loading="lazy"
       decoding="async"
       @load="$emit('load', $event)"
       @error="failed = true; $emit('error', $event)"/>
  <div v-else :class="[className, 'lazy-image-placeholder']" :style="style" role="img" :aria-label="alt">
    <slot><el-icon><Picture/></el-icon></slot>
  </div>
</template>

<script setup>
import {ref, watch} from 'vue'
import {Picture} from '@element-plus/icons-vue'

const props = defineProps({
  src: {type: String, default: ''},
  alt: {type: String, default: ''},
  className: {type: [String, Array, Object], default: ''},
  style: {type: [String, Array, Object], default: undefined}
})
defineEmits(['load', 'error'])
const failed = ref(false)
watch(() => props.src, () => { failed.value = false })
</script>

<style scoped>
.lazy-image-placeholder { display: flex; align-items: center; justify-content: center; background: var(--el-fill-color-light); color: var(--el-text-color-placeholder); }
</style>
