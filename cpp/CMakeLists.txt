cmake_minimum_required(VERSION 3.14)
project(vxsdk_cpp LANGUAGES CXX VERSION 2026.8.26)

set(CMAKE_CXX_STANDARD 17)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

find_package(CURL REQUIRED)

add_library(vxsdk STATIC src/vxsdk.cpp)
target_include_directories(vxsdk PUBLIC ${CMAKE_CURRENT_SOURCE_DIR}/include)
target_link_libraries(vxsdk PUBLIC CURL::libcurl)

# Example / smoke-test binary
add_executable(vxsdk_example examples/example.cpp)
target_link_libraries(vxsdk_example PRIVATE vxsdk)

install(TARGETS vxsdk ARCHIVE DESTINATION lib)
install(DIRECTORY include/ DESTINATION include)
