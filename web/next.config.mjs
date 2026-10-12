/** @type {import('next').NextConfig} */
const nextConfig = {
  reactStrictMode: true,
  // HUI-2626 盲评 P2②：左下浮动「N」是 Next dev 指示器，走查链全程 next dev，
  // 会压叠正文（390 压时间线首行）。生产构建本就不渲染，这里显式关闭。
  devIndicators: false,
};

export default nextConfig;
